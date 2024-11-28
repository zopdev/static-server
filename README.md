# Static Server

A simple and efficient solution for serving static files. This server can be easily deployed via Docker, and it's optimized for serving static websites.

## Features

- **Easy to use**: Just place your static files in the `website` directory, and the server takes care of the rest.
- **Dockerized**: Easily deployable as a Docker container.
- **Lightweight**: Minimal dependencies for optimal performance.
- **Configurable**: You can easily configure the server or extend it based on your needs.

## Requirements

To use this static server, you need the following:
- Docker installed on your system.

## Usage

### 1. Build a Docker image

To deploy the server, you need to build a Docker image using the provided `Dockerfile`.

#### Example `Dockerfile`

```dockerfile
# Use the official static-server image as the base image
# This will pull the latest prebuilt version of the static-server to run your static website
FROM zopdev/static-server:latest

# Copy static files into the container
# The 'COPY' directive moves your static files (in this case, located at '/app/out') into the '/website' directory
# which is where the static server expects to find the files to serve
COPY /app/out /website

# Expose the port on which the server will run
# By default, the server listens on port 8000, so we expose that port to allow access from outside the container
EXPOSE 8000

# Define the command to run the server
# The static server is started with the '/main' binary included in the image, which will start serving
# the files from the '/website' directory on port 8000
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

- The static server is designed to handle a large number of requests efficiently. However, if you have special requirements (such as caching or routing), you may need to customize the server or use additional reverse proxies (like Nginx).
- The server serves all files in the `website` directory, so make sure to avoid any sensitive files or configuration details in that directory.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
