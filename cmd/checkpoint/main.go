// Command checkpoint creates a CRIU checkpoint of a PrestaShop container for fast restore.
//
// This tool is designed to run on a Linux host with Podman and CRIU installed.
// The generated checkpoint can be used by the try-it-now to instantly provision
// new PrestaShop instances via CRIU restore instead of cold-starting containers.
//
// Usage:
//
//	checkpoint [flags]
//
// Example:
//
//	checkpoint --output=/var/lib/checkpoints/prestashop.tar.gz
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CheckpointMetadata contains version and provenance information for a checkpoint.
// This metadata is saved alongside the checkpoint file for validation on restore.
type CheckpointMetadata struct {
	// Version of the metadata format
	Version string `json:"version"`

	// Image is the container image used to create the checkpoint
	Image string `json:"image"`

	// ImageDigest is the full digest of the image (sha256:...)
	ImageDigest string `json:"image_digest,omitempty"`

	// CreatedAt is when the checkpoint was created
	CreatedAt time.Time `json:"created_at"`

	// CRIUVersion is the CRIU version used
	CRIUVersion string `json:"criu_version,omitempty"`

	// PodmanVersion is the Podman version used
	PodmanVersion string `json:"podman_version,omitempty"`

	// Host information
	Host string `json:"host,omitempty"`

	// Database configuration (sanitized - no passwords)
	DBHost   string `json:"db_host,omitempty"`
	DBName   string `json:"db_name,omitempty"`
	DBPrefix string `json:"db_prefix,omitempty"`
}

type config struct {
	// Container settings
	image         string
	containerName string
	port          int

	// Database settings
	mysqlHost string
	mysqlUser string
	mysqlPass string
	mysqlDB   string
	dbPrefix  string

	// Output
	checkpointPath string

	// Behavior
	keepContainer bool
	timeout       time.Duration
}

func main() {
	cfg := parseFlags()

	log.SetFlags(log.Ltime)
	log.Printf("PrestaShop CRIU Checkpoint Creator")
	log.Printf("===================================")

	ctx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
	defer cancel()

	// Step 1: Clean up any existing container with the same name
	log.Printf("Step 1: Cleaning up any existing container...")
	cleanupContainer(cfg.containerName)

	// Step 2: Start PrestaShop container
	log.Printf("Step 2: Starting PrestaShop container...")
	if err := startContainer(ctx, cfg); err != nil {
		log.Fatalf("Failed to start container: %v", err)
	}

	// Step 3: Wait for PrestaShop to be ready
	log.Printf("Step 3: Waiting for PrestaShop to be ready (this may take 1-2 minutes)...")
	url := fmt.Sprintf("http://localhost:%d/", cfg.port)
	if err := waitForHealth(ctx, url, 5*time.Minute); err != nil {
		cleanupContainer(cfg.containerName)
		log.Fatalf("Container failed to become healthy: %v", err)
	}
	log.Printf("  PrestaShop is ready!")

	// Step 4: Warm caches
	log.Printf("Step 4: Warming caches...")
	warmCaches(cfg.port)

	// Step 5: Create CRIU checkpoint
	log.Printf("Step 5: Creating CRIU checkpoint...")
	if err := createCheckpoint(ctx, cfg); err != nil {
		if !cfg.keepContainer {
			cleanupContainer(cfg.containerName)
		}
		log.Fatalf("Failed to create checkpoint: %v", err)
	}

	// Step 5b: Generate metadata file
	log.Printf("Step 5b: Generating checkpoint metadata...")
	if err := generateMetadata(cfg); err != nil {
		log.Printf("  Warning: Failed to generate metadata: %v", err)
	}

	// Step 6: Cleanup (checkpoint command stops the container)
	if !cfg.keepContainer {
		log.Printf("Step 6: Cleaning up container...")
		cleanupContainer(cfg.containerName)
	} else {
		log.Printf("Step 6: Keeping container (--keep-container flag set)")
	}

	// Report results
	if info, err := os.Stat(cfg.checkpointPath); err == nil {
		log.Printf("")
		log.Printf("Success! Checkpoint created:")
		log.Printf("  Path: %s", cfg.checkpointPath)
		log.Printf("  Size: %.2f MB", float64(info.Size())/(1024*1024))
		if _, err := os.Stat(cfg.checkpointPath + ".json"); err == nil {
			log.Printf("  Metadata: %s.json", cfg.checkpointPath)
		}
		log.Printf("")
		log.Printf("To restore:")
		log.Printf("  podman container restore --import=%s --name=demo-test", cfg.checkpointPath)
	} else {
		log.Printf("Warning: Could not verify checkpoint file: %v", err)
	}
}

func parseFlags() *config {
	cfg := &config{}

	// Container settings
	flag.StringVar(&cfg.image, "image", "prestashop/prestashop-flashlight:9.0.0", "PrestaShop Docker image")
	flag.StringVar(&cfg.containerName, "name", "prestashop-checkpoint-template", "Container name")
	flag.IntVar(&cfg.port, "port", 8888, "Host port to expose")

	// Database settings
	flag.StringVar(&cfg.mysqlHost, "mysql-host", "host.containers.internal", "MySQL host (use host.containers.internal for host DB)")
	flag.StringVar(&cfg.mysqlUser, "mysql-user", "demo", "MySQL username")
	flag.StringVar(&cfg.mysqlPass, "mysql-pass", "devpass", "MySQL password")
	flag.StringVar(&cfg.mysqlDB, "mysql-db", "prestashop_demos", "MySQL database name")
	flag.StringVar(&cfg.dbPrefix, "db-prefix", "template_", "Database table prefix for template")

	// Output
	flag.StringVar(&cfg.checkpointPath, "output", "/var/lib/checkpoints/prestashop.tar.gz", "Output path for checkpoint archive")

	// Behavior
	flag.BoolVar(&cfg.keepContainer, "keep-container", false, "Keep container running after checkpoint")
	flag.DurationVar(&cfg.timeout, "timeout", 10*time.Minute, "Overall operation timeout")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [flags]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Creates a CRIU checkpoint of a PrestaShop container for fast restore.\n\n")
		fmt.Fprintf(os.Stderr, "This tool requires:\n")
		fmt.Fprintf(os.Stderr, "  - Linux host (CRIU doesn't work in macOS VMs for complex containers)\n")
		fmt.Fprintf(os.Stderr, "  - Podman installed with CRIU support\n")
		fmt.Fprintf(os.Stderr, "  - Root access (sudo) for checkpoint creation\n")
		fmt.Fprintf(os.Stderr, "  - MariaDB accessible at the specified host\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()
	return cfg
}

func startContainer(ctx context.Context, cfg *config) error {
	args := []string{
		"run", "-d",
		"--name", cfg.containerName,
		"-p", fmt.Sprintf("%d:80", cfg.port),
		"-e", "PS_DOMAIN=template.internal",
		"-e", fmt.Sprintf("MYSQL_HOST=%s", cfg.mysqlHost),
		"-e", fmt.Sprintf("MYSQL_USER=%s", cfg.mysqlUser),
		"-e", fmt.Sprintf("MYSQL_PASSWORD=%s", cfg.mysqlPass),
		"-e", fmt.Sprintf("MYSQL_DATABASE=%s", cfg.mysqlDB),
		"-e", fmt.Sprintf("DB_PREFIX=%s", cfg.dbPrefix),
		"-e", "PS_DEV_MODE=0",
		cfg.image,
	}

	cmd := exec.CommandContext(ctx, "podman", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Printf("  Running: podman %s", strings.Join(args[:6], " ")+"...")
	return cmd.Run()
}

func waitForHealth(ctx context.Context, url string, timeout time.Duration) error {
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(timeout)
	attempts := 0

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		attempts++
		resp, err := client.Get(url)
		if err == nil {
			// Accept any non-5xx response as "healthy enough"
			if resp.StatusCode < 500 {
				resp.Body.Close()
				log.Printf("  Ready after %d attempts (status: %d)", attempts, resp.StatusCode)
				return nil
			}
			resp.Body.Close()
		}

		if attempts%10 == 0 {
			log.Printf("  Still waiting... (attempt %d)", attempts)
		}
		time.Sleep(2 * time.Second)
	}

	return fmt.Errorf("timeout after %d attempts waiting for %s", attempts, url)
}

func warmCaches(port int) {
	client := &http.Client{Timeout: 30 * time.Second}

	pages := []string{
		"/",                      // Homepage
		"/en/2-home-accessories", // Category page (may 404, that's ok)
		"/en/",                   // Alternative homepage
	}

	for _, page := range pages {
		url := fmt.Sprintf("http://localhost:%d%s", port, page)
		resp, err := client.Get(url)
		if err != nil {
			log.Printf("  Warning: Failed to warm %s: %v", page, err)
			continue
		}
		// Drain body to ensure full response is received
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		log.Printf("  Warmed: %s (status: %d)", page, resp.StatusCode)
	}
}

func createCheckpoint(ctx context.Context, cfg *config) error {
	// Ensure output directory exists
	dir := cfg.checkpointPath[:strings.LastIndex(cfg.checkpointPath, "/")]
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	args := []string{
		"container", "checkpoint",
		"--export", cfg.checkpointPath,
		"--tcp-established",
		cfg.containerName,
	}

	// Try without sudo first, then with sudo
	cmd := exec.CommandContext(ctx, "podman", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Printf("  Running: podman %s", strings.Join(args, " "))
	err := cmd.Run()
	if err != nil {
		// Try with sudo
		log.Printf("  Retrying with sudo...")
		cmd = exec.CommandContext(ctx, "sudo", append([]string{"podman"}, args...)...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err = cmd.Run()
	}

	return err
}

func cleanupContainer(name string) {
	// Stop if running
	exec.Command("podman", "stop", name).Run()
	// Remove container
	exec.Command("podman", "rm", "-f", name).Run()
}

// generateMetadata creates a metadata file alongside the checkpoint.
func generateMetadata(cfg *config) error {
	meta := CheckpointMetadata{
		Version:   "1.0",
		Image:     cfg.image,
		CreatedAt: time.Now().UTC(),
		DBHost:    cfg.mysqlHost,
		DBName:    cfg.mysqlDB,
		DBPrefix:  cfg.dbPrefix,
	}

	// Get image digest
	if digest, err := getImageDigest(cfg.image); err == nil {
		meta.ImageDigest = digest
	}

	// Get CRIU version
	if ver, err := getCRIUVersion(); err == nil {
		meta.CRIUVersion = ver
	}

	// Get Podman version
	if ver, err := getPodmanVersion(); err == nil {
		meta.PodmanVersion = ver
	}

	// Get hostname
	if host, err := os.Hostname(); err == nil {
		meta.Host = host
	}

	// Write metadata file
	metaPath := cfg.checkpointPath + ".json"
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	if err := os.WriteFile(metaPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write metadata: %w", err)
	}

	log.Printf("  Metadata saved: %s", metaPath)
	return nil
}

// getImageDigest returns the digest of a container image.
func getImageDigest(image string) (string, error) {
	cmd := exec.Command("podman", "image", "inspect", "--format", "{{.Digest}}", image)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// getCRIUVersion returns the installed CRIU version.
func getCRIUVersion() (string, error) {
	cmd := exec.Command("criu", "--version")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0]), nil
	}
	return strings.TrimSpace(string(out)), nil
}

// getPodmanVersion returns the installed Podman version.
func getPodmanVersion() (string, error) {
	cmd := exec.Command("podman", "--version")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
