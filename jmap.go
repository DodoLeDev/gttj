package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"io"
	"net/http"
	"strings"
	"time"
	"crypto/tls"
)

// JMAPClient represents a JMAP client
type JMAPClient struct {
	baseURL     string
	apiURL      string
	uploadURL   string
	downloadURL string
	authURL     string
	username    string
	password    string
	httpClient  *http.Client
	session     *JMAPSession
	debug       bool
	noSSLCheck  bool
}

// JMAPSession represents a JMAP session
type JMAPSession struct {
	Capabilities    map[string]interface{} `json:"capabilities"`
	Accounts        map[string]JMAPAccount `json:"accounts"`
	PrimaryAccounts map[string]string      `json:"primaryAccounts"`
	Username        string                 `json:"username"`
	APIURL          string                 `json:"apiUrl"`
	DownloadURL     string                 `json:"downloadUrl"`
	UploadURL       string                 `json:"uploadUrl"`
	EventSourceURL  string                 `json:"eventSourceUrl"`
	State           string                 `json:"state"`
}

// JMAPAccount represents a JMAP account
type JMAPAccount struct {
	Name                string                 `json:"name"`
	IsPersonal          bool                   `json:"isPersonal"`
	IsReadOnly          bool                   `json:"isReadOnly"`
	AccountCapabilities map[string]interface{} `json:"accountCapabilities"`
}

// JMAPMailbox represents a JMAP mailbox
type JMAPMailbox struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parentId,omitempty"`
	Role     string `json:"role,omitempty"`
}

// NewJMAPClient creates a new JMAP client
func NewJMAPClient(baseURL string, username string, password string, debug bool, noSSLCheck bool) (*JMAPClient, error) {
	client := &JMAPClient{
		baseURL:    baseURL,
		username:   username,
		password:   password,
		httpClient: &http.Client{},
		debug:      debug,
		noSSLCheck: noSSLCheck,
	}

	// Discover JMAP endpoints and session
	if err := client.discoverEndpoints(); err != nil {
		return nil, fmt.Errorf("failed to discover JMAP endpoints: %w", err)
	}

	return client, nil
}

// debugLog logs a message if debug mode is enabled
func (c *JMAPClient) debugLog(format string, args ...interface{}) {
	if c.debug {
		fmt.Printf("[DEBUG] "+format+"\n", args...)
	}
}

// doJMAPRequest performs a JMAP HTTP request with detailed logging
func (c *JMAPClient) doJMAPRequest(method, url string, body []byte, contentType string) ([]byte, error) {

	if c.noSSLCheck {
		http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	c.debugLog("JMAP Request:\n  Method: %s\n  URL: %s\n  Content-Type: %s", method, url, contentType)
	if body != nil {
		c.debugLog("  Request Body:\n%s", string(body))
	}

	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.SetBasicAuth(c.username, c.password)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	c.debugLog("JMAP Response:\n  Status: %d\n  Response Body:\n%s", resp.StatusCode, string(respBody))

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request returned status %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// discoverEndpoints discovers JMAP endpoints using the well-known URL
func (c *JMAPClient) discoverEndpoints() error {
	wellKnownURL := fmt.Sprintf("%s/.well-known/jmap", c.baseURL)
	c.debugLog("Discovering JMAP endpoints from: %s", wellKnownURL)

	respBody, err := c.doJMAPRequest("GET", wellKnownURL, nil, "")
	if err != nil {
		return fmt.Errorf("failed to get well-known JMAP URL: %w", err)
	}

	var discovery struct {
		APIURL          string                 `json:"apiUrl"`
		UploadURL       string                 `json:"uploadUrl"`
		DownloadURL     string                 `json:"downloadUrl"`
		AuthURL         string                 `json:"authUrl"`
		Capabilities    map[string]interface{} `json:"capabilities"`
		Accounts        map[string]JMAPAccount `json:"accounts"`
		PrimaryAccounts map[string]string      `json:"primaryAccounts"`
		Username        string                 `json:"username"`
		State           string                 `json:"state"`
	}

	if err := json.Unmarshal(respBody, &discovery); err != nil {
		return fmt.Errorf("failed to decode discovery response: %w", err)
	}

	c.debugLog("Discovered JMAP endpoints:\n  API URL: %s\n  Upload URL: %s\n  Download URL: %s\n  Auth URL: %s",
		discovery.APIURL, discovery.UploadURL, discovery.DownloadURL, discovery.AuthURL)

	c.apiURL = discovery.APIURL
	c.uploadURL = discovery.UploadURL
	c.downloadURL = discovery.DownloadURL
	c.authURL = discovery.AuthURL

	// Create session object from discovery response
	c.session = &JMAPSession{
		Capabilities:    discovery.Capabilities,
		Accounts:        discovery.Accounts,
		PrimaryAccounts: discovery.PrimaryAccounts,
		Username:        discovery.Username,
		APIURL:          discovery.APIURL,
		DownloadURL:     discovery.DownloadURL,
		UploadURL:       discovery.UploadURL,
		State:           discovery.State,
	}

	// If no primary account is specified, use the first available account
	if accountId, ok := c.session.PrimaryAccounts["urn:ietf:params:jmap:mail"]; !ok || accountId == "" {
		// Find the first account that has mail capability
		for id, account := range c.session.Accounts {
			if caps, ok := account.AccountCapabilities["urn:ietf:params:jmap:mail"]; ok && caps != nil {
				c.session.PrimaryAccounts["urn:ietf:params:jmap:mail"] = id
				c.debugLog("Using account %s as primary mail account", id)
				break
			}
		}
	}

	// Verify we have a primary account for mail
	if accountId, ok := c.session.PrimaryAccounts["urn:ietf:params:jmap:mail"]; !ok || accountId == "" {
		return fmt.Errorf("no mail-capable account found")
	}

	c.debugLog("Got JMAP session with primary account: %s", c.session.PrimaryAccounts["urn:ietf:params:jmap:mail"])
	return nil
}

// GetMailboxes gets the list of mailboxes from the JMAP server
func (c *JMAPClient) GetMailboxes(starterID string) (map[string]JMAPMailbox, error) {
	c.debugLog("Getting mailboxes from JMAP server")

	var starterIDValue = []interface{}{starterID}
	if starterID == "" {
		starterIDValue = nil
	}

	reqBody := struct {
		Using       []string        `json:"using"`
		MethodCalls [][]interface{} `json:"methodCalls"`
	}{
		Using: []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		MethodCalls: [][]interface{}{
			{
				"Mailbox/get",
				map[string]interface{}{
					"accountId": c.session.PrimaryAccounts["urn:ietf:params:jmap:mail"],
					"ids": starterIDValue,
				},
				"0",
			},
		},
	}

	reqBodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal mailbox request: %w", err)
	}

	respBody, err := c.doJMAPRequest("POST", c.apiURL, reqBodyBytes, "application/json")
	if err != nil {
		return nil, fmt.Errorf("failed to get mailboxes: %w", err)
	}

	var result struct {
		MethodResponses [][]interface{} `json:"methodResponses"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to decode mailbox response: %w", err)
	}

	// Extract mailboxes from the response
	mailboxes := make(map[string]JMAPMailbox)
	if len(result.MethodResponses) > 0 {
		response := result.MethodResponses[0]
		if len(response) > 1 {
			if list, ok := response[1].(map[string]interface{}); ok {
				if listData, ok := list["list"].([]interface{}); ok {
					for _, m := range listData {
						if mailbox, ok := m.(map[string]interface{}); ok {
							var mb JMAPMailbox
							if id, ok := mailbox["id"].(string); ok {
								mb.ID = id
							}
							if name, ok := mailbox["name"].(string); ok {
								mb.Name = name
							}
							if role, ok := mailbox["role"].(string); ok {
								mb.Role = role
							}
							if parentID, ok := mailbox["parentId"].(string); ok {
								mb.ParentID = parentID
								parentName, error := c.GetMailboxes(parentID)
								if error != nil {
									fmt.Errorf("Error occurred when fetching parent: %w", err)
								} else {
									for key, _ := range parentName {
										mb.Name = key + "/" + mb.Name
										break
									}
								}
							}
							mailboxes[mb.Name] = mb
							c.debugLog("Found mailbox: %s (ID: %s, Role: %s)", mb.Name, mb.ID, mb.Role)
						}
					}
				}
			}
		}
	}

	return mailboxes, nil
}

// UploadMessage uploads a message to the JMAP server
func (c *JMAPClient) UploadMessage(threadInfo MessageThreadInfo, messageContent string, mailboxes map[string]JMAPMailbox, whitelist []string) error {
	// Clean up messageId by removing any whitespace
	messageId := strings.TrimSpace(threadInfo.MessageID)

	// In case of whitelist, check if the message is allowed to be migrated
	if (len(whitelist) > 0) {
		if c.debug { fmt.Printf("[DEBUG] Checking if the message %s is in whitelist...", messageId) }

		isAllowed := false
		for _, WLentry := range whitelist {
			if (WLentry == messageId) {
				isAllowed = true
				if c.debug { fmt.Printf("Allowed!\n") }
				break
			}
		}
		if !isAllowed {
			if c.debug { fmt.Printf("Rejected!\n") }
			return nil
		}
	}

	c.debugLog("Processing message ID: %s", messageId)

	// First, upload the message content
	uploadURL := strings.Replace(c.uploadURL, "{accountId}", c.session.PrimaryAccounts["urn:ietf:params:jmap:mail"], 1)
	c.debugLog("Uploading message to: %s", uploadURL)

	// Upload the complete RFC822 message as-is
	respBody, err := c.doJMAPRequest("POST", uploadURL, []byte(messageContent), "message/rfc822")
	if err != nil {
		return fmt.Errorf("failed to upload message: %w", err)
	}

	var uploadResult struct {
		BlobID string `json:"blobId"`
	}
	if err := json.Unmarshal(respBody, &uploadResult); err != nil {
		return fmt.Errorf("failed to decode upload response: %w", err)
	}

	c.debugLog("Successfully uploaded message, got blobId: %s", uploadResult.BlobID)

	// Find the correct mailbox ID
	mailboxID := ""
	if mb, ok := mailboxes[threadInfo.Mailbox]; ok {
		mailboxID = mb.ID
	} else {
		// Try to find by role
		for _, mb := range mailboxes {
			if mb.Role == threadInfo.Mailbox {
				mailboxID = mb.ID
				break
			}
		}
	}

	if mailboxID == "" {
		return fmt.Errorf("mailbox %s not found", threadInfo.Mailbox)
	}

	// Now create the email
	accountId := c.session.PrimaryAccounts["urn:ietf:params:jmap:mail"]
	if accountId == "" {
		return fmt.Errorf("no primary account found for mail capability")
	}

	// Create a map of mailbox IDs for the message
	mailboxIds := make(map[string]bool)
	mailboxIds[mailboxID] = true

	// Create keywords map
	keywordMap := make(map[string]bool)
	for _, keyword := range threadInfo.Keywords {
		keywordMap[keyword] = true
	}
	// Add $replied keyword if the message has been replied to
	if threadInfo.IsRepliedTo {
		keywordMap["$replied"] = true
	}

	emailReq := struct {
		Using       []string        `json:"using"`
		MethodCalls [][]interface{} `json:"methodCalls"`
	}{
		Using: []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		MethodCalls: [][]interface{}{
			{
				"Email/import",
				map[string]interface{}{
					"accountId": accountId,
					"emails": map[string]interface{}{
						uploadResult.BlobID: map[string]interface{}{
							"blobId":     uploadResult.BlobID,
							"mailboxIds": mailboxIds,
							"keywords":   keywordMap,
							"messageId":  messageId,
							"parse":      true,
							"receivedAt": threadInfo.ReceivedAt.Format(time.RFC3339),
						},
					},
				},
				"0",
			},
		},
	}

	emailReqBody, err := json.Marshal(emailReq)
	if err != nil {
		return fmt.Errorf("failed to marshal email request: %w", err)
	}

	respBody, err = c.doJMAPRequest("POST", c.apiURL, emailReqBody, "application/json")
	if err != nil {
		return fmt.Errorf("failed to create email: %w", err)
	}

	// Parse the response to check if the message was created
	var result struct {
		MethodResponses [][]interface{} `json:"methodResponses"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("failed to parse email import response: %w", err)
	}

	if len(result.MethodResponses) > 0 {
		response := result.MethodResponses[0]
		if len(response) > 1 {
			if importResult, ok := response[1].(map[string]interface{}); ok {
				// Check if the message was created
				if created, ok := importResult["created"].(map[string]interface{}); ok {
					if _, ok := created[uploadResult.BlobID]; ok {
						c.debugLog("Message %s was imported successfully", messageId)
						return nil
					}
				}
			}
		}
	}

	return fmt.Errorf("unexpected response format for email import")
}

// CreateMailbox creates a new mailbox in the JMAP server
func (c *JMAPClient) CreateMailbox(name, role string, parentID interface{}) (*JMAPMailbox, error) {
	c.debugLog("Creating mailbox: %s (role: %s)", name, role)

	// Convert empty role to null for JSON
	var roleValue interface{} = role
	if role == "" {
		roleValue = nil
	}

	var parentIDValue interface{} = parentID
	if parentID == "" {
		parentIDValue = nil
	}

	reqBody := struct {
		Using       []string        `json:"using"`
		MethodCalls [][]interface{} `json:"methodCalls"`
	}{
		Using: []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		MethodCalls: [][]interface{}{
			{
				"Mailbox/set",
				map[string]interface{}{
					"accountId": c.session.PrimaryAccounts["urn:ietf:params:jmap:mail"],
					"create": map[string]interface{}{
						"new": map[string]interface{}{
							"name": name,
							"role": roleValue,
							"parentId": parentIDValue,
						},
					},
				},
				"0",
			},
		},
	}

	reqBodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal mailbox creation request: %w", err)
	}

	c.debugLog("Mailbox creation request body: %s", string(reqBodyBytes))

	req, err := http.NewRequest("POST", c.apiURL, bytes.NewReader(reqBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create mailbox creation request: %w", err)
	}

	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Content-Type", "application/json")

	c.debugLog("Sending mailbox creation request")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create mailbox: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		c.debugLog("Mailbox creation failed with status %d: %s", resp.StatusCode, string(body))
		return nil, fmt.Errorf("mailbox creation returned status %d: %s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)
	c.debugLog("Mailbox creation response: %s", string(body))

	var result struct {
		MethodResponses [][]interface{} `json:"methodResponses"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to decode mailbox creation response: %w", err)
	}

	// Extract the created mailbox ID
	if len(result.MethodResponses) > 0 {
		response := result.MethodResponses[0]
		if len(response) > 1 {
			if created, ok := response[1].(map[string]interface{}); ok {
				if createdData, ok := created["created"].(map[string]interface{}); ok {
					if newMailbox, ok := createdData["new"].(map[string]interface{}); ok {
						if id, ok := newMailbox["id"].(string); ok {
							c.debugLog("Successfully created mailbox: %s (ID: %s)", name, id)
							return &JMAPMailbox{
								ID:   id,
								Name: name,
								Role: role,
							}, nil
						}
					}
				}
			}
		}
	}

	return nil, fmt.Errorf("failed to extract created mailbox ID from response: %s", string(body))
}

// GenerateFolderTree ensures all the folders are created and bound to their parent folder (if any)
func (c *JMAPClient) GenerateFolderTree(folderName string, mailboxes map[string]JMAPMailbox) string {
	// If we are not at the root of the folderTree, fetch the parent folder ID through recursivity
	realFolderName := folderName
	isFolderTree := strings.Contains(folderName, "/")
	lastSlash := -1
	if isFolderTree {
	    lastSlash = strings.LastIndex(folderName, "/")
		realFolderName = folderName[lastSlash+1:]
	}

	// Check if the folder exists
	exists := false
	for _, mb := range mailboxes {
		if (folderName == mb.Name) {
			exists = true
			return mb.ID
		}
	}

	if !exists {
		parentID := ""
		parentString := ""
		if isFolderTree {
			parentID = c.GenerateFolderTree(folderName[:lastSlash], mailboxes)
			parentString = " (with parent ID = " + parentID + ")"
		}

		var parentIDValue interface{} = parentID
		if parentID == "" {
			parentIDValue = nil
		}

		newMailbox, err := c.CreateMailbox(realFolderName, "", parentIDValue)
		fmt.Printf("Creating folder: %s%s\n", realFolderName, parentString)

		if err != nil {
			fmt.Errorf("failed to create folder %s: %w", realFolderName, err)
			return ""
		}

		mailboxes[folderName] = *newMailbox

		return newMailbox.ID
	}
	return ""  // Unreachable code
}

// EnsureRequiredMailboxes ensures that all required mailboxes exist
func (c *JMAPClient) EnsureRequiredMailboxes(messageHeaders []*MessageHeaders, reservedFolders []string) (map[string]JMAPMailbox, error) {
	// Get existing mailboxes
	mailboxes, err := c.GetMailboxes("")
	if err != nil {
		return nil, fmt.Errorf("failed to get existing mailboxes: %w", err)
	}

	// Define required mailboxes and their roles
	requiredMailboxes := map[string]string{
		"Inbox":         "inbox",
		"Sent Items":    "sent",
		"Drafts":        "drafts",
		"Archive":       "archive",
		"Junk Mail":     "junk",
		"Deleted Items": "trash",
	}

	// Check each required mailbox
	for name, role := range requiredMailboxes {
		// Check if mailbox exists by name or role
		exists := false
		for _, mb := range mailboxes {
			if mb.Name == name || mb.Role == role {
				exists = true
				break
			}
		}

		if !exists {
			fmt.Printf("Creating missing mailbox: %s (role: %s)\n", name, role)
			newMailbox, err := c.CreateMailbox(name, role, "")
			if err != nil {
				return nil, fmt.Errorf("failed to create mailbox %s: %w", name, err)
			}
			mailboxes[name] = *newMailbox
		}
	}

	// Create mailboxes for categories and folders found in the messages
	categories := make(map[string]bool)
	otherFolders := make(map[string]bool)
	for _, headers := range messageHeaders {
		for _, label := range headers.GmailLabels {
			if strings.Contains(label, ":") {
				// Extract the category name after the colon
				parts := strings.SplitN(label, ":", 2)
				if len(parts) == 2 {
					categories[parts[1]] = true
				}
			} else if !slices.Contains(reservedFolders, label) {
				otherFolders[label] = true
			}
		}
	}

	// Create mailboxes for each category
	for category := range categories {
		// Check if mailbox exists
		exists := false
		for _, mb := range mailboxes {
			if mb.Name == category {
				exists = true
				break
			}
		}

		if !exists {
			fmt.Printf("Creating category mailbox: %s\n", category)
			newMailbox, err := c.CreateMailbox(category, "", "")
			if err != nil {
				return nil, fmt.Errorf("failed to create category mailbox %s: %w", category, err)
			}
			mailboxes[category] = *newMailbox
		}
	}

	// Create mailboxes for each label, while keeping arborescence
	for folderTree := range otherFolders {
		c.GenerateFolderTree(folderTree, mailboxes);
	}

	return mailboxes, nil
}
