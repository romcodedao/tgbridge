package telegram

// Only the fields tgbridge reads are declared.

type User struct {
	ID                        int64  `json:"id"`
	IsBot                     bool   `json:"is_bot"`
	FirstName                 string `json:"first_name"`
	Username                  string `json:"username"`
	HasTopicsEnabled          bool   `json:"has_topics_enabled"`
	AllowsUsersToCreateTopics bool   `json:"allows_users_to_create_topics"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type ForumTopicCreated struct {
	Name string `json:"name"`
}

type Message struct {
	Date              int64              `json:"date"`
	MessageID         int                `json:"message_id"`
	MessageThreadID   int                `json:"message_thread_id,omitempty"`
	IsTopicMessage    bool               `json:"is_topic_message,omitempty"`
	From              *User              `json:"from,omitempty"`
	Chat              Chat               `json:"chat"`
	Text              string             `json:"text,omitempty"`
	Caption           string             `json:"caption,omitempty"`
	ReplyToMessage    *Message           `json:"reply_to_message,omitempty"`
	ForwardOrigin     map[string]any     `json:"forward_origin,omitempty"`
	MediaGroupID      string             `json:"media_group_id,omitempty"`
	ForumTopicCreated *ForumTopicCreated `json:"forum_topic_created,omitempty"`
	Photo             []PhotoSize        `json:"photo,omitempty"`
	Document          *Document          `json:"document,omitempty"`
	// presence-only: unsupported media kinds
	Voice     *struct{} `json:"voice,omitempty"`
	Video     *struct{} `json:"video,omitempty"`
	Audio     *struct{} `json:"audio,omitempty"`
	Sticker   *struct{} `json:"sticker,omitempty"`
	VideoNote *struct{} `json:"video_note,omitempty"`
}

type PhotoSize struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

type Document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data"`
}

type GenerationStopped struct {
	Chat            Chat  `json:"chat"`
	MessageThreadID int   `json:"message_thread_id,omitempty"`
	DraftID         int64 `json:"draft_id"`
}

type ChatMemberUpdated struct {
	Chat          Chat `json:"chat"`
	From          User `json:"from"`
	NewChatMember struct {
		Status string `json:"status"`
	} `json:"new_chat_member"`
}

type Update struct {
	UpdateID                 int                `json:"update_id"`
	Message                  *Message           `json:"message,omitempty"`
	EditedMessage            *Message           `json:"edited_message,omitempty"`
	CallbackQuery            *CallbackQuery     `json:"callback_query,omitempty"`
	StoppedMessageGeneration *GenerationStopped `json:"stopped_message_generation,omitempty"`
	MyChatMember             *ChatMemberUpdated `json:"my_chat_member,omitempty"`
}

type InlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

type InlineKeyboard struct {
	InlineKeyboard [][]InlineButton `json:"inline_keyboard"`
}
