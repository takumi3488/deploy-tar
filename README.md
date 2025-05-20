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

### API Endpoints

#### File Upload

**Request**

```
POST /upload # Upload and extract a tar file
```

**Parameters**

- `path`: Destination directory path where tar contents will be extracted (required)
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
