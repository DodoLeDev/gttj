package main

import (
	"bufio"
	"bytes"
	"fmt"
	"mime"
	"regexp"
	"strings"
)

// MessageHeaders contains the parsed headers we're interested in
type MessageHeaders struct {
	MessageID     string
	References    []string
	InReplyTo     string
	GmailLabels   []string
	GmailThreadID string
	RawContent    string // Store the original message content
}

// parseHeader parses a single header line, handling continuation lines
func parseHeader(scanner *bufio.Scanner) ([]struct{ Key, Value string }, error) {
	// First collect all header lines until we hit a blank line
	var headerLines []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			break
		}
		headerLines = append(headerLines, line)
	}

	// Process continuation lines
	var processedLines []string
	var currentLine string
	for _, line := range headerLines {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			// This is a continuation line, append to current line
			currentLine += " " + strings.TrimLeft(line, " \t")
		} else {
			// This is a new header line
			if currentLine != "" {
				processedLines = append(processedLines, currentLine)
			}
			currentLine = line
		}
	}
	// Don't forget the last line
	if currentLine != "" {
		processedLines = append(processedLines, currentLine)
	}

	var headers []struct{ Key, Value string }
	decoder := new(mime.WordDecoder)
	for _, line := range processedLines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])

		// Decode the value if it's quoted-printable
		decodedValue, err := decoder.DecodeHeader(value)
		if err == nil {
			value = decodedValue
		}

		headers = append(headers, struct{ Key, Value string }{key, value})
	}

	return headers, scanner.Err()
}

// ParseMessageHeaders parses the headers of a message and returns the relevant information
func ParseMessageHeaders(message string) (*MessageHeaders, error) {
	headers := &MessageHeaders{
		RawContent: message, // Store the original message content
	}

	// Create a scanner that splits on CRLF
	scanner := bufio.NewScanner(strings.NewReader(message))
	scanner.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}

		// Look for CRLF
		if i := bytes.Index(data, []byte("\r\n")); i >= 0 {
			return i + 2, data[0:i], nil
		}

		// If we're at EOF, return the rest
		if atEOF {
			return len(data), data, nil
		}

		// Request more data
		return 0, nil, nil
	})

	parsedHeaders, err := parseHeader(scanner)
	if err != nil {
		return nil, err
	}

	// Print each header as it's parsed
	for _, header := range parsedHeaders {
		// Convert header name to lowercase for case-insensitive matching
		headerName := header.Key

		switch {
		case headerName == "message-id":
			headers.MessageID = header.Value
		case headerName == "references":
			// Split references on whitespace and clean up
			refs := strings.Fields(header.Value)
			for _, ref := range refs {
				if ref != "" {
					headers.References = append(headers.References, ref)
				}
			}
		case headerName == "in-reply-to":
			headers.InReplyTo = header.Value
		case headerName == "x-gmail-labels":
			headers.GmailLabels = strings.Split(header.Value, ",")
			// Clean up labels and transform format
			for i, label := range headers.GmailLabels {
				label = strings.TrimSpace(label)
				// Use regex to match the pattern (.*):? *""(.*)""
				re := regexp.MustCompile(`^(.*?):?\s*""(.*)""$`)
				if matches := re.FindStringSubmatch(label); matches != nil {
					category := strings.TrimSpace(matches[1])
					headers.GmailLabels[i] = category + ":" + matches[2]
				} else {
					headers.GmailLabels[i] = label
				}
			}
		case headerName == "x-gm-thrid":
			headers.GmailThreadID = header.Value
		}
	}

	if debug {
		// Print headers in a legible form
		fmt.Printf("\nParsed Headers:\n")
		fmt.Printf("Message-ID: %s\n", headers.MessageID)
		fmt.Printf("In-Reply-To: %s\n", headers.InReplyTo)
		fmt.Printf("References: %s\n", strings.Join(headers.References, ", "))
		fmt.Printf("Gmail Labels: %s\n", strings.Join(headers.GmailLabels, ", "))
		fmt.Printf("Gmail Thread ID: %s\n", headers.GmailThreadID)
		fmt.Println("----------------------------------------")
	}

	return headers, nil
}
