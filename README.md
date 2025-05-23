# Simple File Uploader

A simple file upload server that provides a RESTful API to upload files to a specified directory.

## Features

- Single tar file upload and extraction
- Files extracted to specified paths
- Automatic creation of directory structures from tar archive

## Usage

### Starting the Server

```sh
docker run -p 8080:8080 -v /path/to/local/dir:/path/to/server/dir ghcr.io/takumi3488/simple-file-uploader:latest
```

#### Environment Variables

- `PATH_PREFIX`: (Optional) Restricts the directory paths where files can be uploaded. If set, uploaded files can only be extracted to paths starting with this prefix.
  Example: `docker run -p 8080:8080 -v /path/to/local/dir:/path/to/server/dir -e PATH_PREFIX=/allowed/upload/path ghcr.io/takumi3488/simple-file-uploader:latest`
- `OTEL_EXPORTER_OTLP_ENDPOINT`: The URL of the OTLP endpoint where the OpenTelemetry exporter will send trace data. If this variable is set, OpenTelemetry tracing will be enabled. If not set, tracing will be disabled.
  Example: `http://localhost:4317`
- `OTEL_SERVICE_NAME`: The logical name of the service being instrumented by OpenTelemetry. Defaults to `deploy-tar` if not set.
  Example: `my-custom-service-name`

### API Endpoints

#### File Upload

**Request**

```
POST /upload # Upload and extract a tar file
```

**Parameters**

- `path`: Destination directory path where tar contents will be extracted (required). If the `PATH_PREFIX` environment variable is set, this path must start with the specified prefix, otherwise the request will be rejected.
- `tarfile`: The tar file to upload (required)

**Response**

- Success: 200 OK with message "Tar file extracted successfully"
- Error: 400 or 500 error code with appropriate error message

### Example Usage

Example of uploading a tar file using cURL:

```bash
curl -X POST http://localhost:8080/upload \
  -F "path=/path/to/destination" \
  -F "tarfile=@/path/to/local/archive.tar"
```

## Notes

- If the destination directory does not exist, it will be created automatically
- The server has no file size limit, so be cautious when uploading large files
- This server has no authentication. Implement appropriate authentication for production environments
