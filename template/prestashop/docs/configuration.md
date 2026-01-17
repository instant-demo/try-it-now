# PrestaShop Flashlight Docker Image Configuration

This document describes all environment variables and configuration options for the `prestashop/prestashop-flashlight` Docker image.

> **Note**: This image is for testing/development only. For production, use `prestashop/prestashop`.

## Quick Reference

```yaml
services:
  prestashop:
    image: prestashop/prestashop-flashlight:9.0.0
    environment:
      - PS_DOMAIN=localhost:8000
      - MYSQL_HOST=mysql
      - MYSQL_USER=prestashop
      - MYSQL_PASSWORD=prestashop
      - MYSQL_DATABASE=prestashop
    ports:
      - "8000:80"
```

---

## Domain & Protocol Configuration

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `PS_DOMAIN` | Yes* | - | Public domain and port to reach PrestaShop (e.g., `localhost:8000`). Mutually exclusive with `NGROK_TUNNEL_AUTO_DETECT`. |
| `NGROK_TUNNEL_AUTO_DETECT` | Yes* | - | Ngrok agent base API URL for tunnel domain auto-detection (e.g., `http://ngrok:4040`). Alternative to `PS_DOMAIN`. |
| `PS_PROTOCOL` | No | `http` | Protocol for public URL. Set to `https` for SSL. |
| `SSL_REDIRECT` | No | `false` | If enabled, public URL uses `https://$PS_DOMAIN`. Sets `PS_TRUSTED_PROXIES=127.0.0.1,REMOTE_ADDR`. |

\* One of `PS_DOMAIN` or `NGROK_TUNNEL_AUTO_DETECT` is required.

---

## Database Configuration

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `MYSQL_HOST` | No | `mysql` | MySQL/MariaDB server hostname or IP address. |
| `MYSQL_PORT` | No | `3306` | MySQL/MariaDB server port. |
| `MYSQL_USER` | No | `prestashop` | Database username for connection. |
| `MYSQL_PASSWORD` | No | `prestashop` | Database password for connection. |
| `MYSQL_DATABASE` | No | `prestashop` | Database name to use. |
| `MYSQL_EXTRA_DUMP` | No | (empty) | Path to additional SQL dump file to restore after initialization. |

### Database Compatibility

- **MySQL**: 5.7+
- **MariaDB**: 10.4+ (LTS recommended)

---

## Application Configuration

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `PS_FOLDER` | No | `/var/www/html` | PrestaShop sources directory inside container. |
| `DEBUG_MODE` | No | `false` | Enable PrestaShop debug mode (shows detailed errors). |
| `DRY_RUN` | No | `false` | Exit without starting web server (useful for testing). |

---

## Admin Credentials

Default Back Office access: `{PS_DOMAIN}/admin-dev`

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ADMIN_MAIL_OVERRIDE` | No | `admin@prestashop.com` | Override default admin email address. |
| `ADMIN_PASSWORD_OVERRIDE` | No | `prestashop` | Override default admin password. |

---

## Initialization Scripts

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `INIT_SCRIPTS_DIR` | No | `/tmp/init-scripts` | Directory containing executable scripts to run **before** PrestaShop starts. |
| `INIT_SCRIPTS_ON_RESTART` | No | `false` | Re-run init scripts on container restart. |
| `POST_SCRIPTS_DIR` | No | `/tmp/post-scripts` | Directory containing executable scripts to run **after** PrestaShop starts. |
| `POST_SCRIPTS_ON_RESTART` | No | `false` | Re-run post scripts on container restart. |
| `ON_INIT_SCRIPT_FAILURE` | No | `fail` | Behavior on init script failure: `fail` or `continue`. |
| `ON_POST_SCRIPT_FAILURE` | No | `fail` | Behavior on post script failure: `fail` or `continue`. |

---

## Module Installation

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `INSTALL_MODULES_DIR` | No | (empty) | Directory containing module ZIP files to install. |
| `INSTALL_MODULES_ON_RESTART` | No | `false` | Re-install modules on container restart. |
| `ON_INSTALL_MODULES_FAILURE` | No | `fail` | Behavior on module installation failure: `fail` or `continue`. |

---

## Restart Behavior

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `DUMP_ON_RESTART` | No | `false` | Re-run database dump restoration on container restart. |
| `INIT_ON_RESTART` | No | `false` | Re-run PS_DOMAIN auto-search and dump fixes on restart. |

---

## Development & Debugging

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `XDEBUG_ENABLED` | No | `false` | Enable Xdebug PHP extension for debugging. |
| `BLACKFIRE_ENABLED` | No | `false` | Enable Blackfire PHP profiler. |

---

## Database Isolation Options

### Table Prefix

**Important**: The `prestashop-flashlight` image does **NOT** support table prefix configuration via environment variables. The database dump embedded in the image uses a fixed table prefix (`ps_`).

For multi-tenant database isolation, you have two options:

#### Option 1: Separate Databases (Recommended)

Use a unique `MYSQL_DATABASE` value for each instance:

```yaml
environment:
  - MYSQL_DATABASE=prestashop_instance_abc123
```

This provides complete isolation with no shared tables.

#### Option 2: Custom Init Script with Prefix

Mount a custom init script that modifies the table prefix:

```yaml
volumes:
  - ./init-scripts:/tmp/init-scripts
environment:
  - INIT_SCRIPTS_DIR=/tmp/init-scripts
```

Create `init-scripts/set-prefix.sh`:
```bash
#!/bin/bash
# Modify parameters.php to use custom prefix
sed -i "s/'database_prefix' => 'ps_'/'database_prefix' => '${DB_PREFIX:-ps_}'/" \
    /var/www/html/app/config/parameters.php
```

> **Warning**: Changing table prefix after initial dump restoration requires renaming all existing tables.

---

## Comparison: Flashlight vs Production Image

| Feature | prestashop-flashlight | prestashop/prestashop |
|---------|----------------------|----------------------|
| Use case | Testing/Development | Production |
| Pre-installed | Yes (instant boot) | No (runs installer) |
| MySQL included | No (external required) | Optional internal |
| `DB_PREFIX` support | No | Yes |
| `PS_INSTALL_AUTO` | N/A (pre-installed) | Yes |
| Boot time | ~5-10 seconds | 2-5 minutes |

### Production Image Variables (prestashop/prestashop)

For reference, the production image supports these additional variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `DB_SERVER` | `localhost` | Database server hostname. |
| `DB_USER` | `root` | Database username. |
| `DB_PASSWD` | `admin` | Database password. |
| `DB_NAME` | `prestashop` | Database name. |
| `DB_PREFIX` | `ps_` | **Table prefix for all PrestaShop tables.** |
| `PS_INSTALL_AUTO` | `0` | Run installation automatically. |
| `PS_INSTALL_DB` | `0` | Create the database. |
| `PS_ERASE_DB` | `0` | Drop existing database (destructive). |
| `PS_LANGUAGE` | `en` | Default language. |
| `PS_COUNTRY` | `GB` | Default country. |
| `PS_DEV_MODE` | `0` | Enable development mode. |
| `ADMIN_MAIL` | `demo@prestashop.com` | Admin email. |
| `ADMIN_PASSWD` | `prestashop_demo` | Admin password. |

---

## Multi-Tenant Architecture Considerations

For the Try-It-Now instant provisioning system, database isolation is critical. Here are the strategies:

### Strategy 1: Database-per-Instance (Recommended)

```yaml
# Each demo instance gets a unique database
environment:
  - MYSQL_DATABASE=prestashop_${INSTANCE_ID}
```

**Pros**: Complete isolation, easy cleanup (DROP DATABASE)
**Cons**: Higher resource usage, more databases to manage

### Strategy 2: Shared Database with Instance Tracking

```yaml
# All instances share one database
environment:
  - MYSQL_DATABASE=prestashop_shared
```

Use `id_shop` and shop configuration for isolation within PrestaShop's multi-shop feature.

**Pros**: Resource efficient
**Cons**: Complex cleanup, potential data leakage

### Strategy 3: Pre-built Instance Pool

Pre-create database snapshots and restore them:

```yaml
environment:
  - MYSQL_EXTRA_DUMP=/dumps/instance_${INSTANCE_ID}.sql
```

**Pros**: Fast initialization, isolated data
**Cons**: Storage overhead for dumps

---

## Example Configurations

### Basic Development Setup

```yaml
version: "3"
services:
  prestashop:
    image: prestashop/prestashop-flashlight:9.0.0
    depends_on:
      - mysql
    environment:
      - PS_DOMAIN=localhost:8000
      - DEBUG_MODE=true
    ports:
      - "8000:80"

  mysql:
    image: mariadb:lts
    environment:
      - MYSQL_ROOT_PASSWORD=prestashop
      - MYSQL_USER=prestashop
      - MYSQL_PASSWORD=prestashop
      - MYSQL_DATABASE=prestashop
```

### Multi-Instance with Isolation

```yaml
version: "3"
services:
  prestashop-1:
    image: prestashop/prestashop-flashlight:9.0.0
    environment:
      - PS_DOMAIN=demo1.example.com
      - MYSQL_HOST=mysql
      - MYSQL_DATABASE=prestashop_demo1

  prestashop-2:
    image: prestashop/prestashop-flashlight:9.0.0
    environment:
      - PS_DOMAIN=demo2.example.com
      - MYSQL_HOST=mysql
      - MYSQL_DATABASE=prestashop_demo2
```

### With Custom Initialization

```yaml
services:
  prestashop:
    image: prestashop/prestashop-flashlight:9.0.0
    environment:
      - PS_DOMAIN=localhost:8000
      - INIT_SCRIPTS_DIR=/init
      - ON_INIT_SCRIPT_FAILURE=continue
    volumes:
      - ./init-scripts:/init:ro
```

---

## Available Image Tags

Format: `{prestashop_version}[-{php_version}[-{os}[-{server}]]]`

Examples:
- `9.0.0` - Latest PHP/OS for PrestaShop 9.0.0
- `8.2.1-8.1-fpm-bookworm-apache` - Specific versions
- `latest` - Latest stable release

Check Docker Hub for all available tags:
https://hub.docker.com/r/prestashop/prestashop-flashlight/tags

---

## References

- [PrestaShop Flashlight GitHub](https://github.com/PrestaShop/prestashop-flashlight)
- [Docker Hub](https://hub.docker.com/r/prestashop/prestashop-flashlight)
- [PrestaShop DevDocs - Flashlight](https://devdocs.prestashop-project.org/9/basics/installation/advanced/prestashop-flashlight/)
- [PrestaShop DevDocs - Docker](https://devdocs.prestashop-project.org/9/basics/installation/environments/docker/)
