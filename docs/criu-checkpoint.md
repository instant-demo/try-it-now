# CRIU Checkpoint Creation Guide

This guide explains how to create CRIU checkpoints for PrestaShop containers, enabling sub-second instance provisioning.

## Prerequisites

### System Requirements

1. **Linux operating system** (CRIU is Linux-only)
   - Tested on: Ubuntu 22.04+, Fedora 38+, RHEL 9+

2. **Podman 4.0+** with rootful configuration
   ```bash
   podman --version
   # Must be 4.0 or later
   ```

3. **crun with CRIU support**
   ```bash
   crun --version
   # Look for "+CRIU" in the output, e.g.:
   # crun version 1.14.4
   # commit: ...
   # rundir: /run/user/0/crun
   # spec: 1.0.0
   # +CRIU +YAJL
   ```

4. **CRIU 3.11+**
   ```bash
   criu --version
   # Must be 3.11 or later
   ```

5. **Root access** (required for CRIU memory dumping)

### Installing CRIU

**Ubuntu/Debian:**
```bash
sudo apt-get update
sudo apt-get install criu
```

**Fedora/RHEL:**
```bash
sudo dnf install criu
```

**Building from source** (for latest version):
```bash
git clone https://github.com/checkpoint-restore/criu.git
cd criu
make
sudo make install
```

### Verifying CRIU Works

```bash
# Test CRIU functionality
sudo criu check
# Should output: "Looks good."

# Verify crun has CRIU support
crun --version | grep CRIU
# Should show: +CRIU
```

## Creating a Checkpoint

### Step 1: Configure Podman for Rootful Mode

```bash
# Ensure Podman is running rootful
sudo podman info | grep -i rootless
# Should output: rootless: false
```

### Step 2: Start a PrestaShop Container

```bash
# Pull the PrestaShop flashlight image
sudo podman pull prestashop/prestashop-flashlight:9.0.0

# Start the container with your database configuration
sudo podman run -d \
  --name prestashop-checkpoint \
  --hostname checkpoint \
  -e PS_DOMAIN=checkpoint.localhost \
  -e MYSQL_HOST=your-db-host \
  -e MYSQL_PORT=3306 \
  -e MYSQL_DATABASE=prestashop_demos \
  -e MYSQL_USER=demo \
  -e MYSQL_PASSWORD=yourpassword \
  -e PS_INSTALL_AUTO=1 \
  -p 8080:80 \
  prestashop/prestashop-flashlight:9.0.0
```

### Step 3: Wait for Container to be Ready

```bash
# Monitor logs until PrestaShop is fully initialized
sudo podman logs -f prestashop-checkpoint

# Or poll the health endpoint
while ! curl -s http://localhost:8080/ | grep -q "PrestaShop"; do
  echo "Waiting for PrestaShop..."
  sleep 5
done
echo "PrestaShop ready!"
```

### Step 4: Create the Checkpoint

```bash
# Stop the container gracefully
sudo podman stop prestashop-checkpoint

# Create checkpoint with TCP connections closed
# This allows re-assigning ports and network config on restore
sudo podman container checkpoint prestashop-checkpoint \
  --export /var/lib/checkpoints/prestashop-9.0.0.tar.gz \
  --tcp-close

# Verify checkpoint was created
ls -lh /var/lib/checkpoints/prestashop-9.0.0.tar.gz
```

### Step 5: Test the Checkpoint

```bash
# Remove the original container
sudo podman rm prestashop-checkpoint

# Restore from checkpoint with new port
sudo podman container restore \
  --import /var/lib/checkpoints/prestashop-9.0.0.tar.gz \
  --name prestashop-test \
  --publish-all

# Verify container is running
sudo podman ps | grep prestashop-test

# Test the restored instance
curl -I http://localhost:<new-port>/
```

## Checkpoint Options

### Recommended Flags

| Flag | Purpose |
|------|---------|
| `--tcp-close` | Close TCP connections (required for port reassignment) |
| `--ignore-rootfs` | Don't include rootfs (smaller checkpoint) |
| `--export` | Export to portable tarball |

### Example with All Flags

```bash
sudo podman container checkpoint prestashop-checkpoint \
  --export /var/lib/checkpoints/prestashop-9.0.0.tar.gz \
  --tcp-close
```

## Deploying Checkpoints

### Copying to Production Servers

```bash
# SCP to production server
scp /var/lib/checkpoints/prestashop-9.0.0.tar.gz \
  user@prod-server:/var/lib/try-it-now/checkpoints/

# Or use rsync for larger files
rsync -avz --progress \
  /var/lib/checkpoints/prestashop-9.0.0.tar.gz \
  user@prod-server:/var/lib/try-it-now/checkpoints/
```

### Directory Structure on Server

```
/var/lib/try-it-now/
├── checkpoints/
│   ├── prestashop-9.0.0.tar.gz      # Current checkpoint
│   └── prestashop-9.0.0.tar.gz.md   # Checkpoint metadata (optional)
└── data/
    └── ... (runtime data)
```

### Configuration

Update your `.env` file:

```bash
CONTAINER_MODE=podman
CRIU_ENABLED=true
CHECKPOINT_PATH=/var/lib/try-it-now/checkpoints/prestashop-9.0.0.tar.gz
```

## Troubleshooting

### "CRIU not available" Error

**Symptoms:** Server logs show "CRIU not available - will use Start() fallback"

**Causes and solutions:**

1. **Podman not rootful:**
   ```bash
   sudo podman info | grep rootless
   # If true, switch to rootful mode
   ```

2. **crun missing CRIU support:**
   ```bash
   crun --version | grep CRIU
   # If no "+CRIU", reinstall crun with CRIU support
   ```

3. **Checkpoint file not found:**
   ```bash
   ls -la $CHECKPOINT_PATH
   # Verify file exists and is readable
   ```

### "Failed to restore: overlayfs not supported" Error

**Solution:** Use `--ignore-rootfs` when creating checkpoint or ensure overlayfs is available.

### Restore Fails with Network Errors

**Symptoms:** Container starts but can't connect to database.

**Solutions:**
1. Use `--tcp-close` flag when creating checkpoint
2. Ensure database is accessible from restore environment
3. Check firewall rules

### Slow Restore Performance

**Symptoms:** Restore takes more than 2-3 seconds.

**Possible causes:**
1. Large checkpoint file - reduce by using `--ignore-rootfs`
2. Slow disk I/O - use SSD storage
3. Insufficient memory - ensure adequate RAM

### Permission Denied Errors

**Solution:** Run Podman as root:
```bash
sudo podman ...
```

## Performance Benchmarks

Typical restore times (on modern hardware):

| Operation | Time |
|-----------|------|
| CRIU restore | 0.5-2s |
| Health check pass | 2-5s |
| Total time to ready | 3-7s |

Compare to cold start: 60-120s

## Checkpoint Maintenance

### Updating Checkpoints

When updating PrestaShop or changing configuration:

1. Create new container with updated image/config
2. Wait for full initialization
3. Create new checkpoint
4. Test restore
5. Deploy to production
6. Update `CHECKPOINT_PATH` configuration
7. Restart services

### Checkpoint Versioning

Track checkpoint versions by including metadata:

```bash
# Create metadata file
cat > /var/lib/checkpoints/prestashop-9.0.0.tar.gz.md << EOF
image: prestashop/prestashop-flashlight:9.0.0
created: $(date -Iseconds)
criu_version: $(criu --version | head -1)
podman_version: $(podman --version)
EOF
```

## Security Considerations

1. **Checkpoint files contain memory dumps** - treat as sensitive
2. **Store checkpoints securely** - restrict filesystem permissions
3. **Don't commit checkpoints to git** - use secure artifact storage
4. **Regenerate after security updates** - checkpoints may contain vulnerable code

## References

- [CRIU Documentation](https://criu.org/Main_Page)
- [Podman Checkpoint/Restore](https://podman.io/blogs/2023/06/21/checkpoint-restore.html)
- [Container Migration with CRIU](https://docs.podman.io/en/latest/markdown/podman-container-checkpoint.1.html)
