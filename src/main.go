package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// =============================================================================
// Discord API constants and types
// =============================================================================

const (
	apiBase = "https://discord.com/api/v9"

	// Conservative pacing to reduce transient 400/429 churn.
	searchDelay          = 350 * time.Millisecond
	deleteDelay          = 350 * time.Millisecond
	reactionDelay        = 350 * time.Millisecond
	batchDelay           = 350 * time.Millisecond
	threadDiscoveryDelay = 350 * time.Millisecond
	threadArchiveDelay   = 350 * time.Millisecond
	errorBackoffDelay    = 1250 * time.Millisecond

	maxSearchIndexWaits = 40
)

// Channel types
const (
	ChannelTypeGuildText          = 0
	ChannelTypeDM                 = 1
	ChannelTypeGuildVoice         = 2
	ChannelTypeGroupDM            = 3
	ChannelTypeGuildCategory      = 4
	ChannelTypeGuildNews          = 5
	ChannelTypeGuildNewsThread    = 10
	ChannelTypeGuildPublicThread  = 11
	ChannelTypeGuildPrivateThread = 12
	ChannelTypeGuildStageVoice    = 13
	ChannelTypeGuildForum         = 15
	ChannelTypeGuildMedia         = 16
)

// Relationship types
const (
	RelationshipFriend      = 1
	RelationshipBlocked     = 2
	RelationshipIncomingReq = 3
	RelationshipOutgoingReq = 4
	RelationshipImplicit    = 5
	RelationshipSuggestion  = 6
)

// DiscordClient handles all Discord API interactions via REST (no WebSocket).
type DiscordClient struct {
	token      string
	httpClient *http.Client
	userID     string
	username   string
}

type User struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	Discriminator string `json:"discriminator"`
}

type Guild struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Channel struct {
	ID             string      `json:"id"`
	Type           int         `json:"type"`
	Name           string      `json:"name"`
	GuildID        string      `json:"guild_id"`
	Recipients     []User      `json:"recipients"`
	ThreadMetadata *ThreadMeta `json:"thread_metadata,omitempty"`
}

type ThreadMeta struct {
	Archived         bool   `json:"archived"`
	ArchiveTimestamp string `json:"archive_timestamp"`
}

type Message struct {
	ID        string     `json:"id"`
	Author    User       `json:"author"`
	ChannelID string     `json:"channel_id"`
	Hit       bool       `json:"hit,omitempty"`
	Reactions []Reaction `json:"reactions,omitempty"`
}

type Reaction struct {
	Count int       `json:"count"`
	Me    bool      `json:"me"`
	Emoji EmojiInfo `json:"emoji"`
}

type EmojiInfo struct {
	ID   *string `json:"id"`   // nil for unicode emoji, snowflake string for custom
	Name string  `json:"name"` // unicode character or custom emoji name
}

type SearchResult struct {
	TotalResults int         `json:"total_results"`
	Messages     [][]Message `json:"messages"`
	Retry        bool        `json:"retry"`
}

type RateLimitResponse struct {
	Message    string  `json:"message"`
	RetryAfter float64 `json:"retry_after"`
	Global     bool    `json:"global"`
}

type APIError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

type Relationship struct {
	ID   string `json:"id"`
	Type int    `json:"type"`
	User User   `json:"user"`
}

type ThreadListResponse struct {
	Threads []Channel `json:"threads"`
	HasMore bool      `json:"has_more"`
}

// =============================================================================
// HTTP layer with automatic rate-limit handling
// =============================================================================

func NewDiscordClient(token string) *DiscordClient {
	return &DiscordClient{
		token: token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *DiscordClient) request(method, path string) ([]byte, int, error) {
	return c.requestWithBody(method, path, "")
}

func (c *DiscordClient) requestWithBody(method, path, jsonBody string) ([]byte, int, error) {
	for attempt := 0; attempt < 5; attempt++ {
		var bodyReader io.Reader
		if jsonBody != "" {
			bodyReader = strings.NewReader(jsonBody)
		}

		req, err := http.NewRequest(method, apiBase+path, bodyReader)
		if err != nil {
			return nil, 0, fmt.Errorf("creating request: %w", err)
		}

		req.Header.Set("Authorization", c.token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, 0, fmt.Errorf("executing request: %w", err)
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return body, resp.StatusCode, nil
		}

		if resp.StatusCode == 429 {
			waitTime := 5.0

			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if parsed, err := strconv.ParseFloat(ra, 64); err == nil {
					waitTime = parsed
				}
			}

			if resetAfter := resp.Header.Get("X-RateLimit-Reset-After"); resetAfter != "" {
				if parsed, err := strconv.ParseFloat(resetAfter, 64); err == nil && parsed > waitTime {
					waitTime = parsed
				}
			}

			var rl RateLimitResponse
			if json.Unmarshal(body, &rl) == nil && rl.RetryAfter > 0 {
				if rl.RetryAfter > waitTime {
					waitTime = rl.RetryAfter
				}
			}

			// Add a safety buffer and floor to avoid tight 429 loops (e.g., 0.8s).
			waitTime += 1.0
			if waitTime < 2.0 {
				waitTime = 2.0
			}

			// Thread archive discovery routes are typically stricter than generic GETs.
			if method == "GET" && strings.Contains(path, "/users/@me/threads/archived/private") && waitTime < 6.0 {
				waitTime = 6.0
			}

			scope := ""
			if rl.Global {
				scope = " (global)"
			}

			fmt.Printf("   ⏳ Rate limited%s on %s %s, waiting %.1f seconds (attempt %d/5)...\n", scope, method, path, waitTime, attempt+1)
			time.Sleep(time.Duration(waitTime*1000) * time.Millisecond)
			continue
		}

		return body, resp.StatusCode, nil
	}

	return nil, 429, fmt.Errorf("still rate limited after 5 retries")
}

func parseAPIError(body []byte) APIError {
	var apiErr APIError
	if err := json.Unmarshal(body, &apiErr); err != nil {
		return APIError{}
	}
	return apiErr
}

func formatAPIError(body []byte) string {
	apiErr := parseAPIError(body)
	if apiErr.Message == "" && apiErr.Code == 0 {
		return strings.TrimSpace(string(body))
	}
	if apiErr.Code == 0 {
		return apiErr.Message
	}
	if apiErr.Message == "" {
		return fmt.Sprintf("code %d", apiErr.Code)
	}
	return fmt.Sprintf("code %d: %s", apiErr.Code, apiErr.Message)
}

func olderSnowflakeID(currentOldest, candidate string) string {
	if candidate == "" {
		return currentOldest
	}
	if currentOldest == "" {
		return candidate
	}

	cur, curErr := strconv.ParseUint(currentOldest, 10, 64)
	cand, candErr := strconv.ParseUint(candidate, 10, 64)
	if curErr == nil && candErr == nil {
		if cand < cur {
			return candidate
		}
		return currentOldest
	}

	if len(candidate) < len(currentOldest) {
		return candidate
	}
	if len(candidate) > len(currentOldest) {
		return currentOldest
	}
	if candidate < currentOldest {
		return candidate
	}
	return currentOldest
}

func previousSnowflakeID(id string) string {
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil || n == 0 {
		return id
	}
	return strconv.FormatUint(n-1, 10)
}

// =============================================================================
// Discord API methods — Authentication & Discovery
// =============================================================================

func (c *DiscordClient) Authenticate() error {
	body, status, err := c.request("GET", "/users/@me")
	if err != nil {
		return fmt.Errorf("authentication request failed: %w", err)
	}

	if status == 401 {
		return fmt.Errorf("invalid token — authentication failed (HTTP 401)")
	}
	if status != 200 {
		return fmt.Errorf("unexpected status %d: %s", status, string(body))
	}

	var user User
	if err := json.Unmarshal(body, &user); err != nil {
		return fmt.Errorf("parsing user info: %w", err)
	}

	c.userID = user.ID
	c.username = user.Username
	return nil
}

func (c *DiscordClient) GetAllGuilds() ([]Guild, error) {
	var allGuilds []Guild
	afterID := ""

	for {
		path := "/users/@me/guilds?limit=200"
		if afterID != "" {
			path += "&after=" + afterID
		}

		body, status, err := c.request("GET", path)
		if err != nil {
			return allGuilds, fmt.Errorf("fetching guilds: %w", err)
		}
		if status != 200 {
			return allGuilds, fmt.Errorf("fetching guilds: HTTP %d — %s", status, string(body))
		}

		var guilds []Guild
		if err := json.Unmarshal(body, &guilds); err != nil {
			return allGuilds, fmt.Errorf("parsing guilds: %w", err)
		}

		if len(guilds) == 0 {
			break
		}

		allGuilds = append(allGuilds, guilds...)
		afterID = guilds[len(guilds)-1].ID

		if len(guilds) < 200 {
			break
		}

		time.Sleep(batchDelay)
	}

	return allGuilds, nil
}

func (c *DiscordClient) GetDMChannels() ([]Channel, error) {
	body, status, err := c.request("GET", "/users/@me/channels")
	if err != nil {
		return nil, fmt.Errorf("fetching DM channels: %w", err)
	}
	if status != 200 {
		return nil, fmt.Errorf("fetching DM channels: HTTP %d — %s", status, string(body))
	}

	var channels []Channel
	if err := json.Unmarshal(body, &channels); err != nil {
		return nil, fmt.Errorf("parsing DM channels: %w", err)
	}

	return channels, nil
}

func (c *DiscordClient) GetRelationships() ([]Relationship, error) {
	body, status, err := c.request("GET", "/users/@me/relationships")
	if err != nil {
		return nil, fmt.Errorf("fetching relationships: %w", err)
	}
	if status != 200 {
		return nil, fmt.Errorf("fetching relationships: HTTP %d — %s", status, string(body))
	}

	var rels []Relationship
	if err := json.Unmarshal(body, &rels); err != nil {
		return nil, fmt.Errorf("parsing relationships: %w", err)
	}

	return rels, nil
}

func (c *DiscordClient) OpenDMChannel(recipientID string) (*Channel, error) {
	jsonBody := fmt.Sprintf(`{"recipient_id":"%s"}`, recipientID)
	body, status, err := c.requestWithBody("POST", "/users/@me/channels", jsonBody)
	if err != nil {
		return nil, fmt.Errorf("opening DM channel: %w", err)
	}

	if status != 200 {
		return nil, fmt.Errorf("opening DM channel: HTTP %d — %s", status, string(body))
	}

	var ch Channel
	if err := json.Unmarshal(body, &ch); err != nil {
		return nil, fmt.Errorf("parsing DM channel: %w", err)
	}

	return &ch, nil
}

// =============================================================================
// Discord API methods — Guild channel & thread discovery
// =============================================================================

// GetGuildChannels fetches all channels in a guild.
func (c *DiscordClient) GetGuildChannels(guildID string) ([]Channel, error) {
	body, status, err := c.request("GET", fmt.Sprintf("/guilds/%s/channels", guildID))
	if err != nil {
		return nil, fmt.Errorf("fetching guild channels: %w", err)
	}
	if status == 403 {
		return nil, nil // No access
	}
	if status != 200 {
		return nil, fmt.Errorf("fetching guild channels: HTTP %d", status)
	}

	var channels []Channel
	if err := json.Unmarshal(body, &channels); err != nil {
		return nil, fmt.Errorf("parsing guild channels: %w", err)
	}

	return channels, nil
}

// GetActiveGuildThreads fetches all active threads in a guild.
func (c *DiscordClient) GetActiveGuildThreads(guildID string) ([]Channel, error) {
	body, status, err := c.request("GET", fmt.Sprintf("/guilds/%s/threads/active", guildID))
	if err != nil {
		return nil, fmt.Errorf("fetching active threads: %w", err)
	}
	if status == 403 {
		return nil, nil
	}
	if status != 200 {
		return nil, fmt.Errorf("fetching active threads: HTTP %d", status)
	}

	var result ThreadListResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing active threads: %w", err)
	}

	return result.Threads, nil
}

// GetArchivedPublicThreads fetches all archived public threads for a channel.
func (c *DiscordClient) GetArchivedPublicThreads(channelID string) ([]Channel, error) {
	return c.getArchivedThreads(channelID, "public")
}

// GetArchivedPrivateThreads fetches all archived private threads for a channel.
func (c *DiscordClient) GetArchivedPrivateThreads(channelID string) ([]Channel, error) {
	return c.getArchivedThreads(channelID, "private")
}

// GetJoinedArchivedPrivateThreads fetches archived private threads that the
// current user has joined. Some guilds only expose private archives here.
func (c *DiscordClient) GetJoinedArchivedPrivateThreads(channelID string) ([]Channel, error) {
	var allThreads []Channel
	before := ""

	for {
		path := fmt.Sprintf("/channels/%s/users/@me/threads/archived/private?limit=100", channelID)
		if before != "" {
			path += "&before=" + before
		}

		body, status, err := c.request("GET", path)
		if err != nil {
			return allThreads, fmt.Errorf("fetching joined archived private threads: %w", err)
		}
		if status == 403 || status == 400 || status == 404 {
			break // No access or endpoint not applicable
		}
		if status != 200 {
			return allThreads, fmt.Errorf("fetching joined archived private threads: HTTP %d", status)
		}

		var result ThreadListResponse
		if err := json.Unmarshal(body, &result); err != nil {
			return allThreads, fmt.Errorf("parsing joined archived private threads: %w", err)
		}

		allThreads = append(allThreads, result.Threads...)

		if !result.HasMore || len(result.Threads) == 0 {
			break
		}

		lastThread := result.Threads[len(result.Threads)-1]
		if lastThread.ThreadMetadata != nil && lastThread.ThreadMetadata.ArchiveTimestamp != "" {
			before = lastThread.ThreadMetadata.ArchiveTimestamp
		} else {
			break
		}

		time.Sleep(batchDelay)
	}

	return allThreads, nil
}

func (c *DiscordClient) getArchivedThreads(channelID, kind string) ([]Channel, error) {
	var allThreads []Channel
	before := ""

	for {
		path := fmt.Sprintf("/channels/%s/threads/archived/%s?limit=100", channelID, kind)
		if before != "" {
			path += "&before=" + before
		}

		body, status, err := c.request("GET", path)
		if err != nil {
			return allThreads, fmt.Errorf("fetching archived %s threads: %w", kind, err)
		}
		if status == 403 || status == 400 {
			break // No access or not applicable
		}
		if status != 200 {
			return allThreads, fmt.Errorf("fetching archived %s threads: HTTP %d", kind, status)
		}

		var result ThreadListResponse
		if err := json.Unmarshal(body, &result); err != nil {
			return allThreads, fmt.Errorf("parsing archived threads: %w", err)
		}

		allThreads = append(allThreads, result.Threads...)

		if !result.HasMore || len(result.Threads) == 0 {
			break
		}

		// Paginate using the last thread's archive_timestamp
		lastThread := result.Threads[len(result.Threads)-1]
		if lastThread.ThreadMetadata != nil {
			before = lastThread.ThreadMetadata.ArchiveTimestamp
		} else {
			break
		}

		time.Sleep(batchDelay)
	}

	return allThreads, nil
}

// discoverAllGuildChannelsAndThreads returns all text-capable channels and
// threads in a guild. This is needed for reaction removal (unlike message
// deletion which uses the search API, there's no search-by-reactor endpoint).
func (c *DiscordClient) discoverAllGuildChannelsAndThreads(guildID string) []string {
	seen := make(map[string]bool)
	var channelIDs []string

	addChannel := func(id string) {
		if !seen[id] {
			seen[id] = true
			channelIDs = append(channelIDs, id)
		}
	}

	// Get all guild channels
	channels, err := c.GetGuildChannels(guildID)
	if err != nil {
		return channelIDs
	}

	// Filter to text-capable channel types and collect parent channels
	var parentChannelIDs []string
	for _, ch := range channels {
		switch ch.Type {
		case ChannelTypeGuildText, ChannelTypeGuildNews:
			addChannel(ch.ID)
			parentChannelIDs = append(parentChannelIDs, ch.ID)
		case ChannelTypeGuildVoice, ChannelTypeGuildStageVoice:
			addChannel(ch.ID)
		case ChannelTypeGuildNewsThread, ChannelTypeGuildPublicThread, ChannelTypeGuildPrivateThread:
			addChannel(ch.ID)
		case ChannelTypeGuildForum, ChannelTypeGuildMedia:
			// Forum/media channels don't have messages directly,
			// but their threads (posts) do. We'll discover threads below.
			parentChannelIDs = append(parentChannelIDs, ch.ID)
		}
	}

	// Get all active threads in the guild
	activeThreads, err := c.GetActiveGuildThreads(guildID)
	if err == nil {
		for _, t := range activeThreads {
			addChannel(t.ID)
		}
	}

	// Get archived public + private threads for each parent channel
	for _, parentID := range parentChannelIDs {
		pubThreads, err := c.GetArchivedPublicThreads(parentID)
		if err == nil {
			for _, t := range pubThreads {
				addChannel(t.ID)
			}
		}
		time.Sleep(threadArchiveDelay)

		privThreads, err := c.GetArchivedPrivateThreads(parentID)
		if err == nil {
			for _, t := range privThreads {
				addChannel(t.ID)
			}
		}
		time.Sleep(threadArchiveDelay)

		joinedPrivThreads, err := c.GetJoinedArchivedPrivateThreads(parentID)
		if err == nil {
			for _, t := range joinedPrivThreads {
				addChannel(t.ID)
			}
		}

		time.Sleep(threadDiscoveryDelay)
	}

	return channelIDs
}

// =============================================================================
// Discord Data Package
// =============================================================================

func LoadDataPackageChannelIDs(packagePath string) ([]string, error) {
	indexPath := packagePath

	info, err := os.Stat(packagePath)
	if err != nil {
		return nil, fmt.Errorf("cannot access path %s: %w", packagePath, err)
	}

	if info.IsDir() {
		candidate := filepath.Join(packagePath, "messages", "index.json")
		if _, err := os.Stat(candidate); err == nil {
			indexPath = candidate
		} else {
			return nil, fmt.Errorf("could not find messages/index.json in %s", packagePath)
		}
	}

	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, fmt.Errorf("reading index file: %w", err)
	}

	var index map[string]interface{}
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("parsing index.json: %w", err)
	}

	var channelIDs []string
	for id := range index {
		channelIDs = append(channelIDs, id)
	}

	return channelIDs, nil
}

// =============================================================================
// Search and delete methods
// =============================================================================

// SearchGuildMessages uses Discord's search API to find all messages by the
// user in a guild. Covers all text channels, threads, forums, announcements,
// and voice text chat.
func (c *DiscordClient) SearchGuildMessages(guildID string) (int, error) {
	totalDeleted := 0
	indexWaitCount := 0
	maxID := ""
	skippedMessageIDs := make(map[string]bool)

	for {
		path := fmt.Sprintf("/guilds/%s/messages/search?author_id=%s&include_nsfw=true&sort_by=timestamp&sort_order=desc", guildID, c.userID)
		if maxID != "" {
			path += "&max_id=" + maxID
		}

		body, status, err := c.request("GET", path)
		if err != nil {
			return totalDeleted, fmt.Errorf("search request: %w", err)
		}

		if status == 202 {
			indexWaitCount++
			if indexWaitCount >= maxSearchIndexWaits {
				return totalDeleted, fmt.Errorf("search index not ready after %d retries", maxSearchIndexWaits)
			}
			fmt.Printf("   ⏳ Search index building, waiting (%d/%d)...\n", indexWaitCount, maxSearchIndexWaits)
			time.Sleep(3 * time.Second)
			continue
		}
		indexWaitCount = 0

		if status == 403 {
			fmt.Printf("   ⚠️  No permission to search this server, skipping.\n")
			return totalDeleted, nil
		}

		if status != 200 {
			return totalDeleted, fmt.Errorf("search returned HTTP %d: %s", status, string(body))
		}

		var result SearchResult
		if err := json.Unmarshal(body, &result); err != nil {
			return totalDeleted, fmt.Errorf("parsing search results: %w", err)
		}
		if result.Retry {
			indexWaitCount++
			if indexWaitCount >= maxSearchIndexWaits {
				return totalDeleted, fmt.Errorf("search index requested retry too many times")
			}
			fmt.Printf("   ⏳ Search requested retry, waiting (%d/%d)...\n", indexWaitCount, maxSearchIndexWaits)
			time.Sleep(3 * time.Second)
			continue
		}
		indexWaitCount = 0

		if result.TotalResults == 0 || len(result.Messages) == 0 {
			break
		}

		fmt.Printf("   📊 %d messages remaining...\n", result.TotalResults)

		deletedThisRound := 0
		oldestHitID := ""
		seenInThisPage := make(map[string]bool)

		for _, msgGroup := range result.Messages {
			for _, msg := range msgGroup {
				if msg.Author.ID == c.userID && msg.Hit {
					oldestHitID = olderSnowflakeID(oldestHitID, msg.ID)

					if msg.ID == "" || seenInThisPage[msg.ID] || skippedMessageIDs[msg.ID] {
						continue
					}
					seenInThisPage[msg.ID] = true

					if msg.ChannelID == "" {
						skippedMessageIDs[msg.ID] = true
						continue
					}

					delBody, delStatus, err := c.request("DELETE", fmt.Sprintf("/channels/%s/messages/%s", msg.ChannelID, msg.ID))
					if err != nil {
						fmt.Printf("   ⚠️  Failed to delete message %s: %v\n", msg.ID, err)
						time.Sleep(errorBackoffDelay)
					} else if delStatus == 204 || delStatus == 200 {
						totalDeleted++
						deletedThisRound++
					} else if delStatus == 404 {
						deletedThisRound++
						skippedMessageIDs[msg.ID] = true
					} else if delStatus == 403 {
						fmt.Printf("   ⚠️  Cannot delete message %s (no permission)\n", msg.ID)
						skippedMessageIDs[msg.ID] = true
					} else if delStatus == 400 {
						detail := formatAPIError(delBody)
						if detail != "" {
							fmt.Printf("   ⚠️  Cannot delete message %s (HTTP 400, %s)\n", msg.ID, detail)
						} else {
							fmt.Printf("   ⚠️  Cannot delete message %s (HTTP 400)\n", msg.ID)
						}
						skippedMessageIDs[msg.ID] = true
						time.Sleep(errorBackoffDelay)
					} else {
						detail := formatAPIError(delBody)
						if detail != "" {
							fmt.Printf("   ⚠️  Unexpected status %d deleting message %s (%s)\n", delStatus, msg.ID, detail)
						} else {
							fmt.Printf("   ⚠️  Unexpected status %d deleting message %s\n", delStatus, msg.ID)
						}
						time.Sleep(errorBackoffDelay)
					}

					time.Sleep(deleteDelay)
				}
			}
		}

		if oldestHitID == "" {
			break
		}

		nextMaxID := previousSnowflakeID(oldestHitID)
		if nextMaxID == maxID {
			break
		}
		maxID = nextMaxID

		if deletedThisRound == 0 {
			fmt.Printf("   ⚠️  No deletions in this page; continuing deeper into older history.\n")
		}

		time.Sleep(searchDelay)
	}

	// Discord search can occasionally miss old indexed content. If a guild-level
	// search found nothing, do an exhaustive channel-by-channel history walk.
	if totalDeleted == 0 {
		totalDeleted += c.deepScanGuildMessages(guildID)
	}

	return totalDeleted, nil
}

func (c *DiscordClient) deepScanGuildMessages(guildID string) int {
	channelIDs := c.discoverAllGuildChannelsAndThreads(guildID)
	if len(channelIDs) == 0 {
		return 0
	}

	fmt.Printf("   🔁 Running exhaustive channel scan (%d channels/threads)...\n", len(channelIDs))

	totalDeleted := 0
	for i, chID := range channelIDs {
		count, err := c.iterateAndDeleteChannel(chID)
		if err != nil {
			continue
		}
		totalDeleted += count
		if count > 0 {
			fmt.Printf("      ✅ Deleted %d messages in deep scan channel %d/%d\n", count, i+1, len(channelIDs))
		}
		time.Sleep(batchDelay)
	}

	if totalDeleted > 0 {
		fmt.Printf("   ✅ Deep scan recovered %d additional messages.\n", totalDeleted)
	}

	return totalDeleted
}

// SearchDMMessages uses Discord's search API to find and delete all messages
// in a DM or group DM channel.
func (c *DiscordClient) SearchDMMessages(channelID string) (int, error) {
	totalDeleted := 0
	indexWaitCount := 0
	maxID := ""
	skippedMessageIDs := make(map[string]bool)

	for {
		path := fmt.Sprintf("/channels/%s/messages/search?author_id=%s&sort_by=timestamp&sort_order=desc", channelID, c.userID)
		if maxID != "" {
			path += "&max_id=" + maxID
		}

		body, status, err := c.request("GET", path)
		if err != nil {
			return totalDeleted, fmt.Errorf("search request: %w", err)
		}

		if status == 202 {
			indexWaitCount++
			if indexWaitCount >= maxSearchIndexWaits {
				return totalDeleted, fmt.Errorf("search index not ready after %d retries", maxSearchIndexWaits)
			}
			fmt.Printf("   ⏳ Search index building, waiting (%d/%d)...\n", indexWaitCount, maxSearchIndexWaits)
			time.Sleep(3 * time.Second)
			continue
		}
		indexWaitCount = 0

		if status == 403 || status == 400 || status == 404 {
			fallbackCount, fallbackErr := c.iterateAndDeleteChannel(channelID)
			return totalDeleted + fallbackCount, fallbackErr
		}

		if status != 200 {
			fallbackCount, fallbackErr := c.iterateAndDeleteChannel(channelID)
			if fallbackErr != nil {
				return totalDeleted + fallbackCount, fmt.Errorf("search returned HTTP %d and fallback failed: %w", status, fallbackErr)
			}
			return totalDeleted + fallbackCount, nil
		}

		var result SearchResult
		if err := json.Unmarshal(body, &result); err != nil {
			return totalDeleted, fmt.Errorf("parsing search results: %w", err)
		}
		if result.Retry {
			indexWaitCount++
			if indexWaitCount >= maxSearchIndexWaits {
				return totalDeleted, fmt.Errorf("search index requested retry too many times")
			}
			fmt.Printf("   ⏳ Search requested retry, waiting (%d/%d)...\n", indexWaitCount, maxSearchIndexWaits)
			time.Sleep(3 * time.Second)
			continue
		}
		indexWaitCount = 0

		if result.TotalResults == 0 || len(result.Messages) == 0 {
			break
		}

		fmt.Printf("   📊 %d messages remaining...\n", result.TotalResults)

		deletedThisRound := 0
		oldestHitID := ""
		seenInThisPage := make(map[string]bool)

		for _, msgGroup := range result.Messages {
			for _, msg := range msgGroup {
				if msg.Author.ID == c.userID && msg.Hit {
					oldestHitID = olderSnowflakeID(oldestHitID, msg.ID)

					if msg.ID == "" || seenInThisPage[msg.ID] || skippedMessageIDs[msg.ID] {
						continue
					}
					seenInThisPage[msg.ID] = true

					delBody, delStatus, err := c.request("DELETE", fmt.Sprintf("/channels/%s/messages/%s", channelID, msg.ID))
					if err != nil {
						fmt.Printf("   ⚠️  Failed to delete message %s: %v\n", msg.ID, err)
						time.Sleep(errorBackoffDelay)
					} else if delStatus == 204 || delStatus == 200 {
						totalDeleted++
						deletedThisRound++
					} else if delStatus == 404 {
						deletedThisRound++
						skippedMessageIDs[msg.ID] = true
					} else if delStatus == 403 {
						skippedMessageIDs[msg.ID] = true
						fmt.Printf("   ⚠️  Cannot delete message %s (no permission)\n", msg.ID)
					} else if delStatus == 400 {
						detail := formatAPIError(delBody)
						if detail != "" {
							fmt.Printf("   ⚠️  Cannot delete message %s (HTTP 400, %s)\n", msg.ID, detail)
						} else {
							fmt.Printf("   ⚠️  Cannot delete message %s (HTTP 400)\n", msg.ID)
						}
						skippedMessageIDs[msg.ID] = true
						time.Sleep(errorBackoffDelay)
					} else {
						detail := formatAPIError(delBody)
						if detail != "" {
							fmt.Printf("   ⚠️  Unexpected status %d deleting message %s (%s)\n", delStatus, msg.ID, detail)
						} else {
							fmt.Printf("   ⚠️  Unexpected status %d deleting message %s\n", delStatus, msg.ID)
						}
						time.Sleep(errorBackoffDelay)
					}

					time.Sleep(deleteDelay)
				}
			}
		}

		if oldestHitID == "" {
			break
		}

		nextMaxID := previousSnowflakeID(oldestHitID)
		if nextMaxID == maxID {
			break
		}
		maxID = nextMaxID

		if deletedThisRound == 0 {
			fmt.Printf("   ⚠️  No deletions in this page; continuing deeper into older history.\n")
		}

		time.Sleep(searchDelay)
	}

	return totalDeleted, nil
}

// iterateAndDeleteChannel pages through all messages in a channel and deletes
// the ones authored by the user. Fallback when search API is unavailable.
func (c *DiscordClient) iterateAndDeleteChannel(channelID string) (int, error) {
	totalDeleted := 0
	beforeID := ""

	for {
		path := fmt.Sprintf("/channels/%s/messages?limit=100", channelID)
		if beforeID != "" {
			path += "&before=" + beforeID
		}

		body, status, err := c.request("GET", path)
		if err != nil {
			return totalDeleted, fmt.Errorf("fetching messages: %w", err)
		}

		if status == 403 {
			break
		}

		if status != 200 {
			return totalDeleted, fmt.Errorf("fetching messages: HTTP %d", status)
		}

		var messages []Message
		if err := json.Unmarshal(body, &messages); err != nil {
			return totalDeleted, fmt.Errorf("parsing messages: %w", err)
		}

		if len(messages) == 0 {
			break
		}

		for _, msg := range messages {
			if msg.Author.ID == c.userID {
				_, delStatus, err := c.request("DELETE", fmt.Sprintf("/channels/%s/messages/%s", channelID, msg.ID))
				if err == nil && (delStatus == 204 || delStatus == 200 || delStatus == 404) {
					totalDeleted++
				}
				time.Sleep(deleteDelay)
			}
		}

		beforeID = messages[len(messages)-1].ID

		if len(messages) < 100 {
			break
		}

		time.Sleep(batchDelay)
	}

	return totalDeleted, nil
}

// =============================================================================
// Reaction removal methods
// =============================================================================

// formatEmojiForURL returns the URL-safe string for an emoji to use in API paths.
// Unicode emoji: URL-encoded character (e.g. %F0%9F%91%8D for 👍)
// Custom emoji: name:id (ASCII-safe, no encoding needed)
func formatEmojiForURL(emoji EmojiInfo) string {
	if emoji.ID != nil && *emoji.ID != "" {
		// Custom emoji — name is always alphanumeric/underscore, ID is numeric
		return emoji.Name + ":" + *emoji.ID
	}
	// Unicode emoji — must be URL-encoded
	return url.PathEscape(emoji.Name)
}

// removeReaction removes the current user's reaction from a message.
func (c *DiscordClient) removeReaction(channelID, messageID string, emoji EmojiInfo) error {
	emojiPath := formatEmojiForURL(emoji)
	path := fmt.Sprintf("/channels/%s/messages/%s/reactions/%s/@me", channelID, messageID, emojiPath)

	_, status, err := c.request("DELETE", path)
	if err != nil {
		return err
	}
	if status == 204 || status == 200 || status == 404 {
		return nil // success or already removed
	}
	return fmt.Errorf("HTTP %d", status)
}

// removeReactionsFromChannel iterates through ALL messages in a channel and
// removes any reactions placed by the current user. Returns the number of
// reactions removed.
//
// This must iterate all messages (not just the user's) because reactions can be
// on anyone's messages. There is no Discord API to search by reactor.
func (c *DiscordClient) removeReactionsFromChannel(channelID string) int {
	totalRemoved := 0
	beforeID := ""

	for {
		path := fmt.Sprintf("/channels/%s/messages?limit=100", channelID)
		if beforeID != "" {
			path += "&before=" + beforeID
		}

		body, status, err := c.request("GET", path)
		if err != nil {
			break
		}

		if status == 403 || status == 404 {
			break // No access or channel gone
		}

		if status != 200 {
			break
		}

		var messages []Message
		if err := json.Unmarshal(body, &messages); err != nil {
			break
		}

		if len(messages) == 0 {
			break
		}

		for _, msg := range messages {
			// Check each reaction on this message
			for _, reaction := range msg.Reactions {
				if reaction.Me {
					err := c.removeReaction(channelID, msg.ID, reaction.Emoji)
					if err == nil {
						totalRemoved++
					}
					time.Sleep(reactionDelay)
				}
			}
		}

		beforeID = messages[len(messages)-1].ID

		if len(messages) < 100 {
			break
		}

		time.Sleep(batchDelay)
	}

	return totalRemoved
}

// =============================================================================
// Main purge orchestration
// =============================================================================

// PurgeStats holds detailed statistics about the purge operation
type PurgeStats struct {
	TotalMessagesDeleted   int
	TotalReactionsRemoved  int
	TotalDMMessagesDeleted int
	ServerStats            []ServerStat
	DMChannelsProcessed    int
	TimeElapsed            time.Duration
}

// ServerStat holds per-server statistics
type ServerStat struct {
	GuildID   string
	GuildName string
	Messages  int
	Reactions int
}

// PurgeOptions defines optional scope exclusions for the purge operation.
type PurgeOptions struct {
	ExcludedGuildIDs     map[string]bool
	ExcludedDMChannelIDs map[string]bool
}

func (o PurgeOptions) isGuildExcluded(guildID string) bool {
	return o.ExcludedGuildIDs != nil && o.ExcludedGuildIDs[guildID]
}

func (o PurgeOptions) isDMExcluded(channelID string) bool {
	return o.ExcludedDMChannelIDs != nil && o.ExcludedDMChannelIDs[channelID]
}

func (c *DiscordClient) PurgeAll(dataPackagePath string, options PurgeOptions) PurgeStats {
	totalDeleted := 0
	totalReactionsRemoved := 0
	totalDMMessages := 0
	startTime := time.Now()

	// Track processed DM channel IDs to avoid duplicate work
	processedDMs := make(map[string]bool)

	// Track per-server stats
	var serverStats []ServerStat

	// =========================================================================
	// Phase 1: Server messages via search API
	// =========================================================================
	fmt.Println("📡 Phase 1: Deleting messages from servers (excluding any you skipped)...")
	fmt.Println()

	guilds, err := c.GetAllGuilds()
	if err != nil {
		fmt.Printf("❌ Error fetching servers: %v\n", err)
		guilds = []Guild{} // Initialize empty slice to avoid nil
	} else {
		totalGuildsFound := len(guilds)
		excludedGuildCount := 0

		if len(options.ExcludedGuildIDs) > 0 {
			filtered := make([]Guild, 0, len(guilds))
			for _, guild := range guilds {
				if options.isGuildExcluded(guild.ID) {
					excludedGuildCount++
					continue
				}
				filtered = append(filtered, guild)
			}
			guilds = filtered
		}

		fmt.Printf("✅ Found %d servers.\n", totalGuildsFound)
		if excludedGuildCount > 0 {
			fmt.Printf("   ↪ Excluding %d servers selected by you.\n", excludedGuildCount)
		}
		fmt.Println()

		for i, guild := range guilds {
			name := guild.Name
			if name == "" {
				name = guild.ID
			}
			fmt.Printf("[%d/%d] 🔍 Searching server: %s\n", i+1, len(guilds), name)

			count, err := c.SearchGuildMessages(guild.ID)
			if err != nil {
				fmt.Printf("   ❌ Error: %v\n", err)
			}
			if count > 0 {
				fmt.Printf("   ✅ Deleted %d messages\n", count)
			} else {
				fmt.Printf("   ✓ No messages found\n")
			}
			totalDeleted += count

			// Initialize server stat (reactions will be added in Phase 3)
			serverStats = append(serverStats, ServerStat{
				GuildID:   guild.ID,
				GuildName: name,
				Messages:  count,
				Reactions: 0,
			})
			fmt.Println()
		}
	}

	// =========================================================================
	// Phase 2a: Visible/open DM channels
	// =========================================================================
	fmt.Println("💬 Phase 2a: Deleting messages from open/visible DM channels (excluding any you skipped)...")
	fmt.Println()

	channels, err := c.GetDMChannels()
	if err != nil {
		fmt.Printf("❌ Error fetching DM channels: %v\n", err)
	} else {
		totalOpenDMsFound := len(channels)
		excludedOpenDMCount := 0
		channelsToProcess := channels

		if len(options.ExcludedDMChannelIDs) > 0 {
			channelsToProcess = make([]Channel, 0, len(channels))
			for _, ch := range channels {
				if options.isDMExcluded(ch.ID) {
					excludedOpenDMCount++
					continue
				}
				channelsToProcess = append(channelsToProcess, ch)
			}
		}

		fmt.Printf("✅ Found %d open DM channels.\n", totalOpenDMsFound)
		if excludedOpenDMCount > 0 {
			fmt.Printf("   ↪ Excluding %d DM/group DM channels selected by you.\n", excludedOpenDMCount)
		}
		fmt.Println()

		for i, ch := range channelsToProcess {
			processedDMs[ch.ID] = true
			label := describeChannel(ch)
			fmt.Printf("[%d/%d] 🔍 Processing DM: %s\n", i+1, len(channelsToProcess), label)

			count, err := c.SearchDMMessages(ch.ID)
			if err != nil {
				fmt.Printf("   ❌ Error: %v\n", err)
			}
			if count > 0 {
				fmt.Printf("   ✅ Deleted %d messages\n", count)
			} else {
				fmt.Printf("   ✓ No messages found\n")
			}
			totalDMMessages += count
			totalDeleted += count
			fmt.Println()
		}
	}

	// =========================================================================
	// Phase 2b: Hidden DMs via relationships
	// =========================================================================
	fmt.Println("🔗 Phase 2b: Discovering hidden/closed DMs via relationships...")
	fmt.Println("   (Re-opening DMs with friends, blocked users, and pending requests)")
	fmt.Println()

	rels, err := c.GetRelationships()
	if err != nil {
		fmt.Printf("❌ Error fetching relationships: %v\n", err)
	} else {
		fmt.Printf("✅ Found %d relationships.\n", len(rels))

		discoveredCount := 0
		excludedHiddenDMCount := 0
		for _, rel := range rels {
			ch, err := c.OpenDMChannel(rel.User.ID)
			if err != nil {
				continue
			}

			if processedDMs[ch.ID] {
				continue
			}
			if options.isDMExcluded(ch.ID) {
				excludedHiddenDMCount++
				continue
			}

			discoveredCount++
			processedDMs[ch.ID] = true

			relType := "related"
			switch rel.Type {
			case RelationshipFriend:
				relType = "friend"
			case RelationshipBlocked:
				relType = "blocked"
			case RelationshipIncomingReq:
				relType = "incoming request"
			case RelationshipOutgoingReq:
				relType = "outgoing request"
			}

			fmt.Printf("   🔓 Found hidden DM with %s (%s)\n", rel.User.Username, relType)

			count, err := c.SearchDMMessages(ch.ID)
			if err != nil {
				fmt.Printf("      ❌ Error: %v\n", err)
			}
			if count > 0 {
				fmt.Printf("      ✅ Deleted %d messages\n", count)
			}
			totalDMMessages += count
			totalDeleted += count

			time.Sleep(500 * time.Millisecond)
		}

		if discoveredCount == 0 {
			fmt.Println("   ✓ No additional hidden DMs found (all already processed)")
		}
		if excludedHiddenDMCount > 0 {
			fmt.Printf("   ↪ Skipped %d hidden DM channels from your exclusion list.\n", excludedHiddenDMCount)
		}
		fmt.Println()
	}

	// =========================================================================
	// Phase 2c: DMs from Discord data package (optional)
	// =========================================================================
	if dataPackagePath != "" {
		fmt.Println("📦 Phase 2c: Processing DMs from Discord data package...")
		fmt.Printf("   Loading: %s\n", dataPackagePath)

		packageChannelIDs, err := LoadDataPackageChannelIDs(dataPackagePath)
		if err != nil {
			fmt.Printf("❌ Error loading data package: %v\n", err)
		} else {
			fmt.Printf("✅ Found %d channels in data package.\n", len(packageChannelIDs))

			newChannels := 0
			excludedPackageChannelCount := 0
			for _, chID := range packageChannelIDs {
				if options.isDMExcluded(chID) {
					excludedPackageChannelCount++
					continue
				}
				if processedDMs[chID] {
					continue
				}
				processedDMs[chID] = true
				newChannels++

				fmt.Printf("   🔍 Processing data package channel: %s\n", chID)

				count, err := c.SearchDMMessages(chID)
				if err != nil {
					count, _ = c.iterateAndDeleteChannel(chID)
				}
				if count > 0 {
					fmt.Printf("      ✅ Deleted %d messages\n", count)
				}
				totalDMMessages += count
				totalDeleted += count
			}

			if newChannels == 0 {
				fmt.Println("   ✓ No additional channels found beyond what was already processed")
			}
			if excludedPackageChannelCount > 0 {
				fmt.Printf("   ↪ Skipped %d data package channels from your exclusion list.\n", excludedPackageChannelCount)
			}
			fmt.Println()
		}
	} else {
		fmt.Println("📦 Phase 2c: Discord data package (skipped — not provided)")
		fmt.Println("   For the most complete DM coverage, provide your Discord data export:")
		fmt.Println("   discord-purge --data-package /path/to/package")
		fmt.Println()
	}

	// =========================================================================
	// Phase 3: Remove all reactions from server channels
	// =========================================================================
	fmt.Println("👎 Phase 3: Removing reactions you placed on other people's messages...")
	fmt.Println("   (This requires scanning all messages in all channels — may take a while)")
	fmt.Println()

	// Phase 3a: Server reactions
	for i, guild := range guilds {
		name := guild.Name
		if name == "" {
			name = guild.ID
		}
		fmt.Printf("[%d/%d] 🔍 Scanning server for reactions: %s\n", i+1, len(guilds), name)

		// Discover all text channels + threads in this guild
		channelIDs := c.discoverAllGuildChannelsAndThreads(guild.ID)
		fmt.Printf("   📂 Found %d channels/threads to scan\n", len(channelIDs))

		guildReactions := 0
		for j, chID := range channelIDs {
			removed := c.removeReactionsFromChannel(chID)
			guildReactions += removed
			if removed > 0 {
				fmt.Printf("   ✅ Removed %d reactions from channel %d/%d\n", removed, j+1, len(channelIDs))
			}
		}

		// Update server stats with reaction count
		for i := range serverStats {
			if serverStats[i].GuildID == guild.ID {
				serverStats[i].Reactions = guildReactions
				break
			}
		}

		totalReactionsRemoved += guildReactions
		if guildReactions > 0 {
			fmt.Printf("   ✅ Total: removed %d reactions from this server\n", guildReactions)
		} else {
			fmt.Printf("   ✓ No reactions found\n")
		}
		fmt.Println()
	}

	// Phase 3b: DM reactions
	fmt.Println("   💬 Scanning DM channels for reactions...")
	dmReactionCount := 0
	for chID := range processedDMs {
		removed := c.removeReactionsFromChannel(chID)
		dmReactionCount += removed
		if removed > 0 {
			fmt.Printf("   ✅ Removed %d reactions from DM %s\n", removed, chID)
		}
	}
	totalReactionsRemoved += dmReactionCount

	if dmReactionCount == 0 {
		fmt.Println("   ✓ No DM reactions found")
	}
	fmt.Println()

	// =========================================================================
	// Summary
	// =========================================================================
	elapsed := time.Since(startTime).Round(time.Second)
	fmt.Println(strings.Repeat("=", 70))
	fmt.Println("✅ PURGE COMPLETE!")
	fmt.Println(strings.Repeat("=", 70))
	fmt.Println()
	fmt.Printf("📊 TOTAL MESSAGES DELETED:        %d\n", totalDeleted)
	fmt.Printf("👎 TOTAL REACTIONS REMOVED:       %d\n", totalReactionsRemoved)
	fmt.Printf("💬 TOTAL DM MESSAGES DELETED:     %d\n", totalDMMessages)
	fmt.Println()
	fmt.Println("📈 PER-SERVER BREAKDOWN:")
	fmt.Println(strings.Repeat("-", 70))

	if len(serverStats) == 0 {
		fmt.Println("   No servers processed.")
	} else {
		for _, stat := range serverStats {
			fmt.Printf("   🏠 %s\n", stat.GuildName)
			fmt.Printf("      Messages deleted:  %d\n", stat.Messages)
			fmt.Printf("      Reactions removed: %d\n", stat.Reactions)
			fmt.Println()
		}
	}

	fmt.Println(strings.Repeat("-", 70))
	fmt.Printf("⏱️  Time elapsed:                  %s\n", elapsed)
	fmt.Printf("🏠 Servers processed:             %d\n", len(guilds))
	fmt.Printf("💬 DM channels processed:         %d\n", len(processedDMs))
	fmt.Println(strings.Repeat("=", 70))

	return PurgeStats{
		TotalMessagesDeleted:   totalDeleted,
		TotalReactionsRemoved:  totalReactionsRemoved,
		TotalDMMessagesDeleted: totalDMMessages,
		ServerStats:            serverStats,
		DMChannelsProcessed:    len(processedDMs),
		TimeElapsed:            elapsed,
	}
}

// =============================================================================
// Friend removal and server leaving
// =============================================================================

// RemoveFriend removes a friend relationship.
func (c *DiscordClient) RemoveFriend(userID string) error {
	_, status, err := c.request("DELETE", fmt.Sprintf("/users/@me/relationships/%s", userID))
	if err != nil {
		return err
	}
	if status == 204 || status == 200 {
		return nil
	}
	return fmt.Errorf("HTTP %d", status)
}

// LeaveGuild leaves a server (guild).
func (c *DiscordClient) LeaveGuild(guildID string) error {
	_, status, err := c.request("DELETE", fmt.Sprintf("/users/@me/guilds/%s", guildID))
	if err != nil {
		return err
	}
	if status == 204 || status == 200 {
		return nil
	}
	return fmt.Errorf("HTTP %d", status)
}

// RemoveAllFriends removes all friends from the user's friend list.
func (c *DiscordClient) RemoveAllFriends() (int, error) {
	rels, err := c.GetRelationships()
	if err != nil {
		return 0, fmt.Errorf("fetching relationships: %w", err)
	}

	removedCount := 0
	for _, rel := range rels {
		if rel.Type == RelationshipFriend {
			err := c.RemoveFriend(rel.User.ID)
			if err != nil {
				fmt.Printf("   ⚠️  Failed to remove friend %s: %v\n", rel.User.Username, err)
			} else {
				removedCount++
				fmt.Printf("   ✅ Removed friend: %s\n", rel.User.Username)
			}
			time.Sleep(500 * time.Millisecond) // Rate limit protection
		}
	}

	return removedCount, nil
}

// LeaveAllGuilds leaves all servers the user is a member of.
func (c *DiscordClient) LeaveAllGuilds() (int, error) {
	guilds, err := c.GetAllGuilds()
	if err != nil {
		return 0, fmt.Errorf("fetching guilds: %w", err)
	}

	leftCount := 0
	for _, guild := range guilds {
		name := guild.Name
		if name == "" {
			name = guild.ID
		}
		err := c.LeaveGuild(guild.ID)
		if err != nil {
			fmt.Printf("   ⚠️  Failed to leave server %s: %v\n", name, err)
		} else {
			leftCount++
			fmt.Printf("   ✅ Left server: %s\n", name)
		}
		time.Sleep(500 * time.Millisecond) // Rate limit protection
	}

	return leftCount, nil
}

// describeChannel returns a human-readable label for a DM channel.
func describeChannel(ch Channel) string {
	if len(ch.Recipients) == 0 {
		return fmt.Sprintf("Channel %s", ch.ID)
	}
	if len(ch.Recipients) == 1 {
		r := ch.Recipients[0]
		if r.Discriminator != "" && r.Discriminator != "0" {
			return fmt.Sprintf("%s#%s", r.Username, r.Discriminator)
		}
		return r.Username
	}
	names := make([]string, 0, len(ch.Recipients))
	for _, r := range ch.Recipients {
		names = append(names, r.Username)
	}
	return fmt.Sprintf("Group: %s", strings.Join(names, ", "))
}

func displayGuildName(guild Guild) string {
	if guild.Name != "" {
		return guild.Name
	}
	return guild.ID
}

func parseSelectionInput(input string, max int) (map[int]bool, error) {
	selected := make(map[int]bool)
	normalized := strings.TrimSpace(strings.ToLower(input))
	if normalized == "" || normalized == "none" || normalized == "n" || normalized == "0" {
		return selected, nil
	}
	if normalized == "all" || normalized == "*" {
		for i := 1; i <= max; i++ {
			selected[i] = true
		}
		return selected, nil
	}

	parts := strings.FieldsFunc(normalized, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t'
	})
	if len(parts) == 0 {
		return selected, nil
	}

	for _, part := range parts {
		if part == "" {
			continue
		}

		if strings.Contains(part, "-") {
			rangeParts := strings.Split(part, "-")
			if len(rangeParts) != 2 || rangeParts[0] == "" || rangeParts[1] == "" {
				return nil, fmt.Errorf("invalid range '%s'", part)
			}
			start, err := strconv.Atoi(rangeParts[0])
			if err != nil {
				return nil, fmt.Errorf("invalid number '%s'", rangeParts[0])
			}
			end, err := strconv.Atoi(rangeParts[1])
			if err != nil {
				return nil, fmt.Errorf("invalid number '%s'", rangeParts[1])
			}
			if start > end {
				start, end = end, start
			}
			if start < 1 || end > max {
				return nil, fmt.Errorf("range %d-%d is out of bounds (1-%d)", start, end, max)
			}
			for i := start; i <= end; i++ {
				selected[i] = true
			}
			continue
		}

		idx, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("invalid selection '%s'", part)
		}
		if idx < 1 || idx > max {
			return nil, fmt.Errorf("selection %d is out of bounds (1-%d)", idx, max)
		}
		selected[idx] = true
	}

	return selected, nil
}

func promptSelection(reader *bufio.Reader, prompt string, max int) map[int]bool {
	if max <= 0 {
		return map[int]bool{}
	}

	for {
		fmt.Print(prompt)
		input, _ := reader.ReadString('\n')

		selected, err := parseSelectionInput(input, max)
		if err != nil {
			fmt.Printf("❌ %v\n", err)
			continue
		}
		return selected
	}
}

func promptPurgeOptions(guilds []Guild, dmChannels []Channel) PurgeOptions {
	options := PurgeOptions{
		ExcludedGuildIDs:     make(map[string]bool),
		ExcludedDMChannelIDs: make(map[string]bool),
	}

	fmt.Println("🧭 Optional scope selection")
	fmt.Println("By default, the purge covers everything reachable on your account.")
	fmt.Println("You can exclude specific servers and DM/group DM channels before starting.")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)

	if len(guilds) > 0 {
		fmt.Println("Servers:")
		for i, guild := range guilds {
			fmt.Printf("  [%d] %s (ID: %s)\n", i+1, displayGuildName(guild), guild.ID)
		}
		fmt.Println()

		selectedGuilds := promptSelection(
			reader,
			"Enter server numbers to EXCLUDE (e.g. 1,3-5) or press Enter for none: ",
			len(guilds),
		)
		for i, guild := range guilds {
			if selectedGuilds[i+1] {
				options.ExcludedGuildIDs[guild.ID] = true
			}
		}
	} else {
		fmt.Println("No servers found to list for exclusion.")
	}

	fmt.Println()

	if len(dmChannels) > 0 {
		fmt.Println("Open DM / Group DM channels:")
		for i, ch := range dmChannels {
			channelKind := "DM"
			if ch.Type == ChannelTypeGroupDM {
				channelKind = "Group DM"
			}
			fmt.Printf("  [%d] %s: %s (ID: %s)\n", i+1, channelKind, describeChannel(ch), ch.ID)
		}
		fmt.Println()

		selectedDMs := promptSelection(
			reader,
			"Enter DM/channel numbers to EXCLUDE (e.g. 2,4-6) or press Enter for none: ",
			len(dmChannels),
		)
		for i, ch := range dmChannels {
			if selectedDMs[i+1] {
				options.ExcludedDMChannelIDs[ch.ID] = true
			}
		}
	} else {
		fmt.Println("No open DM channels found to list for exclusion.")
	}

	fmt.Println()
	fmt.Printf(
		"✅ Exclusions selected: %d servers, %d DM/group DM channels.\n",
		len(options.ExcludedGuildIDs),
		len(options.ExcludedDMChannelIDs),
	)
	if len(options.ExcludedGuildIDs) > 0 || len(options.ExcludedDMChannelIDs) > 0 {
		fmt.Println("   Excluded items will be skipped during message deletion and reaction removal.")
	}
	fmt.Println()

	return options
}

// =============================================================================
// User interaction
// =============================================================================

func main() {
	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║          Discord Message Purge Tool                 ║")
	fmt.Println("║          Deletes ALL your messages everywhere       ║")
	fmt.Println("╚══════════════════════════════════════════════════════╝")
	fmt.Println()

	// Parse optional --data-package flag
	dataPackagePath := ""
	for i, arg := range os.Args[1:] {
		if arg == "--data-package" || arg == "-d" {
			if i+1 < len(os.Args[1:]) {
				dataPackagePath = os.Args[i+2]
			} else {
				fmt.Println("❌ --data-package requires a path argument")
				os.Exit(1)
			}
		}
	}

	// Check for token in environment variable first
	token := os.Getenv("DISCORD_TOKEN")
	if token == "" {
		token = promptForToken()
	} else {
		fmt.Println("✅ Using token from DISCORD_TOKEN environment variable.")
		fmt.Println()
	}

	// Clean up token — strip surrounding quotes (common copy-paste issue)
	token = strings.Trim(token, "\" '\t\r\n")

	if token == "" {
		fmt.Println("❌ Error: Token is required.")
		os.Exit(1)
	}

	// Create client and authenticate
	client := NewDiscordClient(token)

	fmt.Println("🔐 Authenticating...")
	err := client.Authenticate()
	if err != nil {
		fmt.Printf("❌ Authentication failed: %v\n", err)
		fmt.Println()
		fmt.Println("Troubleshooting:")
		fmt.Println("  • Make sure you copied the full token")
		fmt.Println("  • Tokens expire — get a fresh one if it's old")
		fmt.Println("  • Don't include quotes around the token")
		os.Exit(1)
	}

	fmt.Printf("✅ Authenticated as: %s (ID: %s)\n", client.username, client.userID)
	fmt.Println()

	purgeOptions := PurgeOptions{
		ExcludedGuildIDs:     make(map[string]bool),
		ExcludedDMChannelIDs: make(map[string]bool),
	}

	fmt.Println("📋 Loading servers and DM channels...")
	selectionGuilds, guildErr := client.GetAllGuilds()
	if guildErr != nil {
		fmt.Printf("⚠️  Could not load server list for exclusions: %v\n", guildErr)
		selectionGuilds = []Guild{}
	}

	selectionDMs, dmErr := client.GetDMChannels()
	if dmErr != nil {
		fmt.Printf("⚠️  Could not load DM channel list for exclusions: %v\n", dmErr)
		selectionDMs = []Channel{}
	}

	if guildErr == nil || dmErr == nil {
		fmt.Println()
		purgeOptions = promptPurgeOptions(selectionGuilds, selectionDMs)
	} else {
		fmt.Println("⚠️  Exclusion selection unavailable; continuing with full deletion scope.")
		fmt.Println()
	}

	// Confirmation
	if !confirmDeletion() {
		fmt.Println("Operation cancelled.")
		os.Exit(0)
	}

	fmt.Println()
	fmt.Println("Starting message purge... This may take a very long time.")
	fmt.Println("You can press Ctrl+C at any time to stop. Already-deleted messages stay deleted.")
	fmt.Println()

	stats := client.PurgeAll(dataPackagePath, purgeOptions)

	// Ask if user wants to remove friends and leave servers
	fmt.Println()
	if confirmCleanup() {
		fmt.Println()
		fmt.Println("🗑️  Removing all friends and leaving all servers...")
		fmt.Println()

		// Remove friends
		fmt.Println("👥 Removing friends...")
		friendsRemoved, err := client.RemoveAllFriends()
		if err != nil {
			fmt.Printf("❌ Error removing friends: %v\n", err)
		} else {
			fmt.Printf("✅ Removed %d friends.\n", friendsRemoved)
		}
		fmt.Println()

		// Leave servers
		fmt.Println("🚪 Leaving servers...")
		serversLeft, err := client.LeaveAllGuilds()
		if err != nil {
			fmt.Printf("❌ Error leaving servers: %v\n", err)
		} else {
			fmt.Printf("✅ Left %d servers.\n", serversLeft)
		}
		fmt.Println()

		fmt.Println(strings.Repeat("=", 70))
		fmt.Println("✅ CLEANUP COMPLETE!")
		fmt.Println(strings.Repeat("=", 70))
		fmt.Println()
		fmt.Printf("📊 Summary:\n")
		fmt.Printf("   • Messages deleted:        %d\n", stats.TotalMessagesDeleted)
		fmt.Printf("   • Reactions removed:       %d\n", stats.TotalReactionsRemoved)
		fmt.Printf("   • DM messages deleted:     %d\n", stats.TotalDMMessagesDeleted)
		fmt.Printf("   • Friends removed:        %d\n", friendsRemoved)
		fmt.Printf("   • Servers left:           %d\n", serversLeft)
		fmt.Println(strings.Repeat("=", 70))
	} else {
		fmt.Println()
		fmt.Println("Cleanup skipped. Friends and servers remain unchanged.")
	}
}

func promptForToken() string {
	fmt.Println("Discord no longer supports username/password login via API.")
	fmt.Println("You need to provide your user token instead.")
	fmt.Println()
	fmt.Println("┌─ How to get your Discord token ─────────────────────┐")
	fmt.Println("│                                                     │")
	fmt.Println("│  1. Open Discord in your browser (discord.com)      │")
	fmt.Println("│  2. Press F12 to open Developer Tools               │")
	fmt.Println("│  3. Go to the Network tab                           │")
	fmt.Println("│  4. Type 'api' in the filter box                    │")
	fmt.Println("│  5. Click on any request to discord.com/api/...     │")
	fmt.Println("│  6. In Headers, find 'authorization'                │")
	fmt.Println("│  7. Copy the token value                            │")
	fmt.Println("│                                                     │")
	fmt.Println("│  Or set the DISCORD_TOKEN environment variable.     │")
	fmt.Println("└─────────────────────────────────────────────────────┘")
	fmt.Println()
	fmt.Print("Enter your Discord user token: ")

	reader := bufio.NewReader(os.Stdin)
	token, _ := reader.ReadString('\n')
	return strings.TrimSpace(token)
}

func confirmDeletion() bool {
	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║  ⚠️  WARNING — DESTRUCTIVE ACTION                   ║")
	fmt.Println("╠══════════════════════════════════════════════════════╣")
	fmt.Println("║                                                     ║")
	fmt.Println("║  This will DELETE your messages and reactions across ║")
	fmt.Println("║  Discord (except any exclusions you selected):       ║")
	fmt.Println("║                                                     ║")
	fmt.Println("║    • All messages in ALL servers                    ║")
	fmt.Println("║    • All threads (public & private)                 ║")
	fmt.Println("║    • All forum posts                                ║")
	fmt.Println("║    • All direct messages (open AND hidden)          ║")
	fmt.Println("║    • All group DMs                                  ║")
	fmt.Println("║    • All reactions you placed on any message        ║")
	fmt.Println("║                                                     ║")
	fmt.Println("║  This action CANNOT be undone!                      ║")
	fmt.Println("║                                                     ║")
	fmt.Println("╚══════════════════════════════════════════════════════╝")
	fmt.Println()
	fmt.Print("Would you like to delete all public and private messages")
	fmt.Print(" you have ever sent from this account? (yes/no): ")

	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))

	return response == "yes" || response == "y"
}

func confirmCleanup() bool {
	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║  ⚠️  ADDITIONAL CLEANUP OPTION                      ║")
	fmt.Println("╠══════════════════════════════════════════════════════╣")
	fmt.Println("║                                                     ║")
	fmt.Println("║  Would you like to also:                            ║")
	fmt.Println("║                                                     ║")
	fmt.Println("║    • Remove ALL friends from your friend list      ║")
	fmt.Println("║    • Leave ALL servers you are a member of         ║")
	fmt.Println("║                                                     ║")
	fmt.Println("║  This action CANNOT be undone!                      ║")
	fmt.Println("║                                                     ║")
	fmt.Println("╚══════════════════════════════════════════════════════╝")
	fmt.Println()
	fmt.Print("Remove all friends and leave all servers? (yes/no): ")

	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))

	return response == "yes" || response == "y"
}
