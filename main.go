package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/mem"
)

// Regular expression to parse From line
// Example: From 1829804852868694154@xxx Sat Apr 19 04:44:52 +0000 2025
var fromLineRegex = regexp.MustCompile(`From \d+@\S+ (\w+) (\w+) (\d+) (\d+:\d+:\d+) \+0000 (\d{4})`)

var debug bool

type ProcessingStats struct {
	TotalMessages int
	StartTime     time.Time
}

func NewProcessingStats() *ProcessingStats {
	return &ProcessingStats{
		StartTime: time.Now(),
	}
}

func (s *ProcessingStats) Update() {
	elapsed := time.Since(s.StartTime).Seconds()
	rate := float64(s.TotalMessages) / elapsed

	v, _ := mem.VirtualMemory()
	heapInUse := float64(v.Used) / 1024 / 1024
	rssTotal := float64(v.Total) / 1024 / 1024

	fmt.Printf("\rProgress: %d messages (%.2f msg/s) | Memory: Heap %.1fMB, RSS %.1fMB",
		s.TotalMessages, rate, heapInUse, rssTotal)
}

func processMboxStream(reader io.Reader, stats *ProcessingStats, processEntity func(time.Time, string) error, messageLimit int) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 50*1024*1024), 100*1024*1024) // 1MB initial size, 10MB max size

	var messageBuilder strings.Builder
	var inMessage bool
	var messageCount int
	var parseErrors int
	var currentDate time.Time

	// Local function to process a single message
	countAndProcessMessage := func() {
		messageCount++
		// We only care about headers, so we can just count the message
		stats.TotalMessages++
		stats.Update()
		if err := processEntity(currentDate, messageBuilder.String()); err != nil {
			parseErrors++
			fmt.Fprintf(os.Stderr, "\nError processing message %d: %v\n", messageCount, err)
		}
		messageBuilder.Reset()
	}

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "From ") {
			// If we were already processing a message, finish it
			if inMessage && messageBuilder.Len() > 0 {
				countAndProcessMessage()
			}

			// Check if we've hit the message limit
			if messageLimit > 0 && stats.TotalMessages >= messageLimit {
				fmt.Printf("\nReached message limit of %d messages\n", messageLimit)
				return nil
			}

			// Parse the date from the From line using regex
			matches := fromLineRegex.FindStringSubmatch(line)
			if len(matches) == 6 {
				// matches[1] = day of week
				// matches[2] = month
				// matches[3] = day
				// matches[4] = time
				// matches[5] = year
				dateStr := fmt.Sprintf("%s %s %s %s %s +0000",
					matches[1], matches[2], matches[3], matches[4], matches[5])
				debugLog("Parsing date: %s", dateStr)
				var err error
				currentDate, err = time.Parse("Mon Jan 2 15:04:05 2006 +0000", dateStr)
				if err != nil {
					fmt.Fprintf(os.Stderr, "\nError parsing date: %s\n", dateStr)
					currentDate = time.Time{}
				} else {
					debugLog("Successfully parsed date: %s", currentDate.Format(time.RFC3339))
				}
			} else {
				fmt.Fprintf(os.Stderr, "\nCould not parse From line: %s\n", line)
				currentDate = time.Time{}
			}

			inMessage = true
			continue // Skip the From line
		}

		if inMessage {
			// Handle escaped From lines (lines starting with >From)
			if strings.HasPrefix(line, ">From ") {
				line = line[1:] // Remove the '>' prefix
			}
			messageBuilder.WriteString(line)
			messageBuilder.WriteString("\r\n") // Use CRLF for line endings
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanner error: %w", err)
	}

	// Process the last message if any
	if inMessage && messageBuilder.Len() > 0 {
		countAndProcessMessage()
	}

	if parseErrors > 0 {
		fmt.Fprintf(os.Stderr, "\nTotal parse errors: %d out of %d messages\n", parseErrors, messageCount)
	}

	return nil
}

func processTakeoutArchive(filePath string, processEntity func(time.Time, string) error, messageLimit int) (int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return 0, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)
	stats := NewProcessingStats()

	// Start progress reporting in the background
	stopProgress := make(chan struct{})
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				stats.Update()
			case <-stopProgress:
				return
			}
		}
	}()

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			close(stopProgress)
			return 0, fmt.Errorf("failed to read tar entry: %w", err)
		}

		if filepath.Ext(header.Name) == ".mbox" {
			if err := processMboxStream(tarReader, stats, processEntity, messageLimit); err != nil {
				close(stopProgress)
				return 0, fmt.Errorf("failed processing %s: %w", header.Name, err)
			}
			// Check if we've hit the message limit
			if messageLimit > 0 && stats.TotalMessages >= messageLimit {
				close(stopProgress)
				return stats.TotalMessages, nil
			}
		}
	}

	close(stopProgress)
	fmt.Println() // New line after progress
	return stats.TotalMessages, nil
}

func matchLocale(messageHeaders []*MessageHeaders) LabelMapping {
	// Analyze Gmail labels to determine locale
	fmt.Println("\nAnalyzing Gmail labels to determine locale...")
	labelCounts := make(map[string]int)
	for _, headers := range messageHeaders {
		for _, label := range headers.GmailLabels {
			labelCounts[label]++
		}
	}

	// Read the label map
	labelMap, err := ReadLabelMap("label-map.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading label map: %v\n", err)
		os.Exit(1)
	}

	// Find the best matching locale
	var bestMatchCount int
	var bestMapping = LabelMapping{}
	for _, mapping := range labelMap.Mappings {
		matchCount := 0
		var matches []string
		// Check roles
		for label := range mapping.Roles {
			if labelCounts[label] > 0 {
				matchCount++
				matches = append(matches, fmt.Sprintf("%s (role)", label))
			}
		}
		// Check keywords
		for label := range mapping.Keywords {
			if labelCounts[label] > 0 {
				matchCount++
				matches = append(matches, fmt.Sprintf("%s (keyword)", label))
			}
		}
		// Check categories
		for label := range mapping.Categories {
			if labelCounts[label] > 0 {
				matchCount++
				matches = append(matches, fmt.Sprintf("%s (category)", label))
			}
		}

		if matchCount > bestMatchCount {
			bestMatchCount = matchCount
			bestMapping = mapping
		}
	}

	return bestMapping
}

// MessageThreadInfo contains information about a message's thread and reply status
type MessageThreadInfo struct {
	MessageID   string
	ThreadID    string
	Mailbox     string
	Keywords    []string
	IsRepliedTo bool
	RepliedToBy []string
	ReceivedAt  time.Time
}

// analyzeMessageThreads analyzes the message headers to determine thread relationships and reply status
func analyzeMessageThreads(headers []*MessageHeaders) []MessageThreadInfo {
	// Create a map to track which messages have been replied to
	repliedToMap := make(map[string][]string)

	// First pass: identify sent messages and their replies
	for _, h := range headers {
		// Check if this is a sent message
		isSent := false
		for _, label := range h.GmailLabels {
			if label == "Sent" || label == "Gesendet" {
				isSent = true
				break
			}
		}

		// If this is a sent message and it has In-Reply-To, mark the original message as replied to
		if isSent && h.InReplyTo != "" {
			repliedToMap[h.InReplyTo] = append(repliedToMap[h.InReplyTo], h.MessageID)
		}
	}

	// Get the label map and detect locale
	labelMap, err := ReadLabelMap("label-map.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading label map: %v\n", err)
		os.Exit(1)
	}
	localeMap := matchLocale(headers)
	locale := localeMap.Locale

	// Second pass: create thread info for each message
	threadInfo := make([]MessageThreadInfo, 0, len(headers))
	for _, h := range headers {
		info := MessageThreadInfo{
			MessageID: h.MessageID,
			ThreadID:  h.GmailThreadID,
			Keywords:  make([]string, 0),
		}

		// Determine mailbox and keywords based on labels
		hasNonInboxRole := false
		hasInboxRole := false
		hasCategory := false
		categoryMailbox := ""
		usedLabels := make(map[string]bool) // Track which labels were used for mailbox determination

		// First pass: check for role mailboxes
		for _, label := range h.GmailLabels {
			// Try to map the label using the label map
			mapped, isMapped, isRole := labelMap.MapLabel(locale, label)
			if isMapped && isRole {
				if mapped == "inbox" {
					hasInboxRole = true
					usedLabels[label] = true
				} else {
					// Non-inbox role found, this is the mailbox
					info.Mailbox = mapped
					hasNonInboxRole = true
					usedLabels[label] = true
					break
				}
			}
		}

		// If no non-inbox role was found, check for inbox role and categories
		if !hasNonInboxRole {
			// Look for category labels
			for _, label := range h.GmailLabels {
				if strings.Contains(label, ":") {
					parts := strings.SplitN(label, ":", 2)
					if len(parts) == 2 {
						hasCategory = true
						categoryMailbox = parts[1]
						usedLabels[label] = true
						break
					}
				}
			}

			// Determine mailbox based on inbox role and categories
			if hasInboxRole {
				if hasCategory {
					info.Mailbox = categoryMailbox
				} else {
					info.Mailbox = "inbox"
				}
			} else {
				// No role found, default to inbox
				info.Mailbox = "inbox"
			}
		}

		// Process remaining labels as keywords
		for _, label := range h.GmailLabels {
			// Skip if this label was used for mailbox determination
			if usedLabels[label] {
				continue
			}

			// Skip if this label is in the ignore list
			if mapping := labelMap.GetMappingByLocale(locale); mapping != nil {
				isIgnored := false
				for _, ignored := range mapping.Ignore {
					if ignored == label {
						isIgnored = true
						break
					}
				}
				if isIgnored {
					continue
				}
			}

			// Try to map the label
			mapped, isMapped, isRole := labelMap.MapLabel(locale, label)
			if isMapped {
				if !isRole {
					// This is a keyword
					info.Keywords = append(info.Keywords, mapped)
				}
			} else {
				// Not mapped, check if it's a category
				if strings.Contains(label, ":") {
					parts := strings.SplitN(label, ":", 2)
					if len(parts) == 2 {
						// This is a category, add it as a keyword
						info.Keywords = append(info.Keywords, parts[1])
					}
				} else {
					// Not mapped, not a category, not ignored - add as keyword
					info.Keywords = append(info.Keywords, label)
				}
			}
		}

		// Check if this message has been replied to
		if replies, exists := repliedToMap[h.MessageID]; exists {
			info.IsRepliedTo = true
			info.RepliedToBy = replies
		}

		threadInfo = append(threadInfo, info)
	}

	return threadInfo
}

// printProcessingStats prints statistics about the message processing
func printProcessingStats(totalMessages int, messageHeaders []*MessageHeaders, elapsed time.Duration) {
	fmt.Printf("\nFirst pass complete:\n")
	fmt.Printf("Total messages processed: %d\n", totalMessages)
	fmt.Printf("Messages with headers: %d\n", len(messageHeaders))
	fmt.Printf("Processing time: %.2f seconds\n", elapsed.Seconds())
	fmt.Printf("Processing rate: %.2f messages/second\n", float64(totalMessages)/elapsed.Seconds())
}

// printThreadAnalysis prints a summary of thread information
func printThreadAnalysis(threadInfo []MessageThreadInfo) {
	// Group messages by thread ID
	threads := make(map[string][]MessageThreadInfo)
	for _, info := range threadInfo {
		threads[info.ThreadID] = append(threads[info.ThreadID], info)
	}

	// Print thread information in a more readable format
	fmt.Printf("\nThread Analysis:\n")
	fmt.Printf("Total threads: %d\n", len(threads))

	// Count messages by mailbox
	mailboxCounts := make(map[string]int)
	for _, info := range threadInfo {
		mailboxCounts[info.Mailbox]++
	}

	fmt.Printf("\nMessages by mailbox:\n")
	for mailbox, count := range mailboxCounts {
		fmt.Printf("  %s: %d messages\n", mailbox, count)
	}

	// Count messages with replies
	repliedCount := 0
	for _, info := range threadInfo {
		if info.IsRepliedTo {
			repliedCount++
		}
	}
	fmt.Printf("\nMessages with replies: %d\n", repliedCount)

	// Print example of a thread with replies
	fmt.Printf("\nExample thread with replies:\n")
	for threadID, messages := range threads {
		hasReplies := false
		for _, msg := range messages {
			if msg.IsRepliedTo {
				hasReplies = true
				break
			}
		}
		if hasReplies {
			fmt.Printf("Thread %s:\n", threadID)
			for _, msg := range messages {
				fmt.Printf("  - %s (%s)", msg.MessageID, msg.Mailbox)
				if msg.IsRepliedTo {
					fmt.Printf(" [Replied to by %d messages]", len(msg.RepliedToBy))
				}
				fmt.Println()
			}
			break // Only show one example
		}
	}
}

func main() {
	// Define command-line flags
	filePath := flag.String("file", "", "Path to the takeout archive file")
	debugFlag := flag.Bool("debug", false, "Enable debug mode")
	limitFlag := flag.Int("limit", 0, "Limit the number of messages to process (0 for no limit)")
	jmapURL := flag.String("jmap", "", "Base URL of the JMAP server")
	flag.Parse()

	// Validate required flags
	if *filePath == "" {
		fmt.Println("Error: --file flag is required")
		flag.Usage()
		os.Exit(1)
	}

	if *jmapURL == "" {
		fmt.Println("Error: --jmap flag is required")
		flag.Usage()
		os.Exit(1)
	}

	debug = *debugFlag
	messageLimit := *limitFlag
	if messageLimit > 0 {
		fmt.Printf("Processing limited to %d messages\n", messageLimit)
	}

	startTime := time.Now()

	// Array to store message headers and their received dates
	var messageHeaders []*MessageHeaders
	var receivedDates []time.Time

	fmt.Println("Starting first pass: parsing message headers...")
	readHeaders := func(receivedDate time.Time, message string) error {
		headers, err := ParseMessageHeaders(message)
		if err != nil {
			return fmt.Errorf("failed to parse headers: %w", err)
		}

		messageHeaders = append(messageHeaders, headers)
		receivedDates = append(receivedDates, receivedDate)
		return nil
	}
	totalMessages, err := processTakeoutArchive(*filePath, readHeaders, messageLimit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Print processing statistics
	printProcessingStats(totalMessages, messageHeaders, time.Since(startTime))

	// Analyze locale and print result
	localeMap := matchLocale(messageHeaders)
	fmt.Printf("Detected locale: %s\n", localeMap.Locale)

	// Analyze and print thread information
	threadInfo := analyzeMessageThreads(messageHeaders)

	// Set the received dates in the thread info
	for i := range threadInfo {
		threadInfo[i].ReceivedAt = receivedDates[i]
		debugLog("Setting received date for message %s: %s", threadInfo[i].MessageID, receivedDates[i].Format(time.RFC3339))
	}

	printThreadAnalysis(threadInfo)

	// Initialize JMAP client
	fmt.Println("\nInitializing JMAP client...")
	jmapClient, err := NewJMAPClient(*jmapURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing JMAP client: %v\n", err)
		os.Exit(1)
	}

	// Get available mailboxes
	fmt.Println("\nQuerying available mailboxes...")
	mailboxes, err := jmapClient.EnsureRequiredMailboxes(messageHeaders)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error ensuring required mailboxes: %v\n", err)
		os.Exit(1)
	}

	// Upload messages to JMAP server
	fmt.Println("\nUploading messages to JMAP server...")
	uploadStart := time.Now()
	uploadedCount := 0
	errorCount := 0

	// Create stats for upload process
	uploadStats := NewProcessingStats()

	// Start progress reporting in the background
	stopProgress := make(chan struct{})
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				uploadStats.Update()
			case <-stopProgress:
				return
			}
		}
	}()

	for i, info := range threadInfo {
		if err := jmapClient.UploadMessage(info, messageHeaders[i].RawContent, mailboxes); err != nil {
			fmt.Fprintf(os.Stderr, "Error uploading message %s: %v\n", info.MessageID, err)
			errorCount++
			continue
		}
		uploadedCount++
		uploadStats.TotalMessages = uploadedCount
	}

	close(stopProgress)
	uploadElapsed := time.Since(uploadStart)
	fmt.Printf("\nUpload complete:\n")
	fmt.Printf("Successfully uploaded: %d messages\n", uploadedCount)
	fmt.Printf("Failed uploads: %d messages\n", errorCount)
	fmt.Printf("Upload time: %.2f seconds\n", uploadElapsed.Seconds())
	fmt.Printf("Upload rate: %.2f messages/second\n", float64(uploadedCount)/uploadElapsed.Seconds())
}
