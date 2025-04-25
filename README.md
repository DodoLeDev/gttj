# Gmail Takeout Tool

This tool processes Gmail takeout archives and imports them into a JMAP server.

## Setup

1. Create a `.env` file in the project root with the following content:
```
JMAP_USERNAME=your_username
JMAP_PASSWORD=your_password
```

2. Build the tool:
```bash
go build
```

## Usage

```bash
./gmail-takeout-tool --file path/to/takeout.tgz --jmap https://your-jmap-server.com
```

### Command Line Options

- `--file`: Path to the Gmail takeout archive file (required)
- `--jmap`: Base URL of the JMAP server (required)
- `--debug`: Enable debug mode
- `--limit`: Limit the number of messages to process (0 for no limit)

## Features

- Processes Gmail takeout archives
- Extracts message headers and content
- Analyzes thread relationships
- Imports messages into a JMAP server
- Preserves mailbox structure and labels
- Tracks reply relationships

## Requirements

- Go 1.23 or later
- A JMAP server with basic authentication
- Gmail takeout archive in .tgz format 