# Gmail Takeout To JSON

This tool processes Gmail takeout archives and imports them into a JMAP server.

This implementation was tested with [stalwart](https://stalw.art/) and may require adaptation if used
with other JMAP implementations.

## Setup

1. Create a `.env` file in the project root with the following content (optional):
```
JMAP_URL=https://your-jmap-server.com
JMAP_USERNAME=your_username
JMAP_PASSWORD=your_password
```

2. Build the tool:
```bash
go build
```

## Usage

```bash
./gmail-takeout-tool -f path/to/takeout.tgz -j https://your-jmap-server.com -u your_username
```

### Command Line Options

- `-f`: Path to the Gmail takeout archive file (required)
- `-j`: Base URL of the JMAP server (required if JMAP_URL not set)
- `-u`: JMAP server username (required if JMAP_USERNAME not set)
- `-d`: Enable debug mode
- `-l`: Limit the number of messages to process (0 for no limit)

### Authentication

The tool supports multiple ways to provide JMAP server credentials:

1. Environment variables:
   - `JMAP_URL`: Base URL of the JMAP server
   - `JMAP_USERNAME`: JMAP server username
   - `JMAP_PASSWORD`: JMAP server password

2. Command line options:
   - `-j`: Base URL of the JMAP server
   - `-u`: JMAP server username

3. Interactive password prompt:
   - If no password is provided via `JMAP_PASSWORD`, the tool will prompt you to enter it interactively

Command line options take precedence over environment variables.

## Features

- Processes Gmail takeout archives
- Extracts message headers and content
- Analyzes thread relationships
- Imports messages into a JMAP server
- Preserves mailbox structure and labels
- Tracks reply relationships

## Label Mapping

The tool includes a label mapping system to handle Gmail's localized labels. The mapping is defined in `label-map.yaml` and supports:

- Role mappings (e.g., "Gesendet" → "sent", "Posteingang" → "inbox")
- Keyword mappings (e.g., "Geöffnet" → "$seen", "Wichtig" → "important")
- Category mappings
- Ignore lists for labels that should be skipped

The tool automatically detects the locale of your Gmail labels and applies the appropriate mapping. Currently, German (de) and English (en) mappings are included by default. To add support for additional languages, you can extend the `label-map.yaml` file with new locale mappings.

## Requirements

- Go 1.23 or later
- A JMAP server with basic authentication
- Gmail takeout archive in .tgz format 