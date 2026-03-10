# Static Server

A simple and efficient solution for serving static files.

## Features

- **Easy to use**: Just place your static files in the `static` directory, and the server takes care of the rest.
- **Dockerized**: Easily deployable as a Docker container.
- **Lightweight**: Minimal dependencies for optimal performance.
- **Configurable**: You can easily configure the server or extend it based on your needs.
  - The server serves files from the `static` directory by default, but you can change this by setting the `STATIC_DIR_PATH` environment variable.
  - Support all the configs of the gofr framework - https://gofr.dev

## Config File Hydration

When the `CONFIG_FILE_PATH` environment variable is set, the server replaces any `${VAR}` placeholders in that file at startup using values from the environment (including `.env` files). The file is rewritten in-place before serving begins.

This is useful for injecting runtime configuration into static front-end apps without rebuilding them.

If any placeholders have no matching environment variable, the server still writes the file (substituting empty strings for missing values) and logs an error listing the unresolved variables.

#### Example

Given a `config.json` template:

```json
{
  "clientId": "${GOOGLE_CLIENT_ID}",
  "apiUrl": "${API_BASE_URL}"
}
```

If `GOOGLE_CLIENT_ID=abc123` and `API_BASE_URL=https://api.example.com` are set, the file becomes:

```json
{
  "clientId": "abc123",
  "apiUrl": "https://api.example.com"
}
```

> See the [example Dockerfile](#1-build-a-docker-image) below for how to set `CONFIG_FILE_PATH`.

## Usage

### 1. Build a Docker image

To deploy the server, you need to build a Docker image using the provided `Dockerfile`.

#### Example `Dockerfile`

```dockerfile
# Use the official static-server image as the base image
# This will pull the prebuilt version of the static-server to run your static website
FROM zopdev/static-server:v0.0.8

# Copy static files into the container
# The 'COPY' directive moves your static files (in this case, located at '/app/out') into the '/static' directory
# which is where the static server expects to find the files to serve
COPY /app/out /static

# Set the path to the config file for environment variable hydration at startup
ENV CONFIG_FILE_PATH=/static/config.json

# The server listens on port 8000 by default; set HTTP_PORT to change it

# Define the command to run the server
# The static server is started with the '/main' binary included in the image
CMD ["/main"]
```

### 2. Build the Docker image

Navigate to your project directory and run the following command to build the Docker image:

```bash
docker build -t static-server .
```

This command:
- Uses the `Dockerfile` in the current directory (`.`) to build an image.
- Tags the image with the name `static-server` (`-t static-server`).

### 3. Run the Docker container

Once the image is built, run the container using the following command:

```bash
docker run -d -p 8000:8000 static-server
```

This command:
- Runs the container in detached mode (`-d`).
- Maps port 8000 on your host machine to port 8000 inside the container (`-p 8000:8000`), so you can access the static files via `http://localhost:8000`.

### 4. Access your static website

Once the container is running, you can visit your website at:

```
http://localhost:8000
```

Your static files will be served, and the root (`/`) will typically display your `index.html` (if present).

## Notes

- The server serves all files in the `static` directory, so make sure to avoid any sensitive files or configuration details in that directory.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
