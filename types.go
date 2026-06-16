// Package dctl is a pure CLI client for the Discord bot REST API (v10).
// Auth is a bot token sent as `Authorization: Bot <token>`. No gateway/websocket:
// every call is on-demand HTTP. Mono-server by design (one bot token, one default
// channel).
package dctl

// Author identifies who wrote a message.
type Author struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Bot      bool   `json:"bot"`
}

// Attachment is a file uploaded alongside a message. URL points at the Discord CDN.
type Attachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Size        int    `json:"size"`
}

// Message is the subset of a Discord message we surface.
type Message struct {
	ID          string       `json:"id"`
	ChannelID   string       `json:"channel_id"`
	Content     string       `json:"content"`
	Author      Author       `json:"author"`
	Timestamp   string       `json:"timestamp"`
	Attachments []Attachment `json:"attachments"`
}

// Guild is a Discord server the bot belongs to.
type Guild struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Channel is a Discord channel. Type 0 is a text channel.
type Channel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    int    `json:"type"`
	GuildID string `json:"guild_id,omitempty"`
}

// Role is a Discord guild role.
type Role struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Color       int    `json:"color"`
	Permissions string `json:"permissions"`
	Position    int    `json:"position"`
}

// GuildMember is a member of a guild.
type GuildMember struct {
	User  Author   `json:"user"`
	Nick  string   `json:"nick"`
	Roles []string `json:"roles"`
}
