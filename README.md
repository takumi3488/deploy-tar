# Simple File Uploader

A simple file upload server that provides both RESTful API and gRPC API to upload files to a specified directory and list directory contents.

## Features

- Single tar file upload and extraction (REST API)
- Upload of regular files (non-archive)
- Files extracted to specified paths
- Automatic creation of directory structures from tar archive
- Directory listing via REST API and gRPC API

## Usage

### Starting the Server

```sh
docker run -p 8080:8080 -p 9090:9090 -v /path/to/local/dir:/path/to/server/dir ghcr.io/takumi3488/simple-file-uploader:latest
```

The server runs on two ports:

- **Port 8080**: REST API (HTTP)
- **Port 9090**: gRPC API

#### Environment Variables

- `PATH_PREFIX`: (Optional) Restricts the directory paths where files can be uploaded. If set, uploaded files can only be extracted to paths starting with this prefix.
  Example: `docker run -p 8080:8080 -v /path/to/local/dir:/path/to/server/dir -e PATH_PREFIX=/allowed/upload/path ghcr.io/takumi3488/simple-file-uploader:latest`
- `OTEL_EXPORTER_OTLP_ENDPOINT`: The URL of the OTLP endpoint where the OpenTelemetry exporter will send trace data. If this variable is set, OpenTelemetry tracing will be enabled. If not set, tracing will be disabled.
  Example: `http://localhost:4317`
- `OTEL_SERVICE_NAME`: The logical name of the service being instrumented by OpenTelemetry. Defaults to `deploy-tar` if not set.
  Example: `my-custom-service-name`

### API Endpoints

#### REST API (Port 8080)

##### File Upload

**Request**

```
POST /upload # Upload and extract a tar file, or upload a regular file
```

**Parameters**

- `path`: Destination directory path where tar contents will be extracted (required). If the `PATH_PREFIX` environment variable is set, this path must start with the specified prefix, otherwise the request will be rejected.
- `tarfile`: The tar file or regular file to upload (required)

**Response**

- Success:
  - For tar files: 200 OK with message "Tar file extracted successfully"
  - For regular files: 200 OK with message "File uploaded successfully"
- Error: 400 or 500 error code with appropriate error message

##### Directory Listing

**Request**

```
GET /list # List contents of a directory
```

**Query Parameters**

- `d`: (Optional) The sub-directory path to list, relative to the `PATH_PREFIX` (if set) or the server's root. If not provided, lists the root directory.
  Example: `d=myfolder` or `d=myfolder%2Fanotherfolder` (URL encoded for nested directories).

**Response**

- Success: 200 OK with an HTML page listing the directory contents.
- Error: 400, 403, 404, or 500 error code with appropriate error message.

#### gRPC API (Port 9090)

##### File Service

The gRPC API provides the `FileService` with the following RPC method:

```protobuf
service FileService {
  rpc ListDirectory(ListDirectoryRequest) returns (ListDirectoryResponse);
}
```

###### ListDirectory

Lists the contents of a directory.

**Request Message (`ListDirectoryRequest`)**

```protobuf
message ListDirectoryRequest {
  string directory = 1; // Optional subdirectory path
}
```

**Response Message (`ListDirectoryResponse`)**

```protobuf
message ListDirectoryResponse {
  string path = 1;                           // Current path
  repeated DirectoryEntry entries = 2;       // Directory entries
  string parent_link = 3;                   // Parent directory link (optional)
}

message DirectoryEntry {
  string name = 1;  // File/directory name
  string type = 2;  // "file" or "directory"
  string size = 3;  // File size (empty for directories)
  string link = 4;  // Relative link path
}
```

**Error Handling**

The gRPC API uses standard gRPC status codes:

- `NOT_FOUND`: Directory not found
- `PERMISSION_DENIED`: Access denied or path traversal attempt
- `INVALID_ARGUMENT`: Invalid parameters
- `INTERNAL`: Internal server error

### Example Usage

**Example Usage**

##### REST API Examples

Example of listing a directory's contents using cURL:

To list the root directory (or `PATH_PREFIX` if set):

```bash
curl http://localhost:8080/list
```

To list a subdirectory named `my_files`:

```bash
curl http://localhost:8080/list?d=my_files
```

To list a nested subdirectory `my_files/documents`:

```bash
curl "http://localhost:8080/list?d=my_files%2Fdocuments"
```

Example of uploading a tar file using cURL:

```bash
curl -X POST http://localhost:8080/upload \
  -F "path=/path/to/destination" \
  -F "tarfile=@/path/to/local/archive.tar"
```

##### gRPC API Examples

Example of using the gRPC API with `grpcurl`:

To list the root directory:

```bash
grpcurl -plaintext -d '{}' localhost:9090 fileservice.FileService/ListDirectory
```

To list a subdirectory:

```bash
grpcurl -plaintext -d '{"directory": "my_files"}' localhost:9090 fileservice.FileService/ListDirectory
```

To explore the gRPC service definition:

```bash
grpcurl -plaintext localhost:9090 describe fileservice.FileService
```

## Notes

- If the destination directory does not exist, it will be created automatically
- The server has no file size limit, so be cautious when uploading large files
- This server has no authentication. Implement appropriate authentication for production environments
