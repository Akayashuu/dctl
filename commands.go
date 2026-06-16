package dctl

import "strconv"

// Discord application-command types.
const (
	cmdChatInput = 1
	cmdUser      = 2
	cmdMessage   = 3
)

// Discord application-command option types.
const (
	optSubCommand      = 1
	optSubCommandGroup = 2
	optString          = 3
	optInteger         = 4
	optBoolean         = 5
	optUser            = 6
	optChannel         = 7
	optRole            = 8
	optMentionable     = 9
	optNumber          = 10
	optAttachment      = 11
)

// Perm is a Discord permission bit, combinable for default_member_permissions.
type Perm uint64

// A subset of Discord permission bits, enough to gate commands. Combine via Perms.
const (
	PermKickMembers     Perm = 1 << 1
	PermBanMembers      Perm = 1 << 2
	PermAdministrator   Perm = 1 << 3
	PermManageChannels  Perm = 1 << 4
	PermManageGuild     Perm = 1 << 5
	PermManageMessages  Perm = 1 << 13
	PermManageRoles     Perm = 1 << 28
	PermManageWebhooks  Perm = 1 << 29
	PermManageThreads   Perm = 1 << 34
	PermModerateMembers Perm = 1 << 40
)

// Channel types not already declared in channels.go, for ChannelTypes.
const (
	ChannelDM    = 1
	ChannelVoice = 2
	ChannelNews  = 5
)

// Command builds a Discord application command.
type Command struct {
	typ     int
	name    string
	desc    string
	nameLoc map[string]string
	descLoc map[string]string
	perms   *string
	dmPerm  *bool
	nsfw    bool
	options []Option
}

// NewCommand builds a CHAT_INPUT (slash) command.
func NewCommand(name, description string) *Command {
	return &Command{typ: cmdChatInput, name: name, desc: description}
}

// NewUserCommand builds a USER context-menu command (no description/options).
func NewUserCommand(name string) *Command { return &Command{typ: cmdUser, name: name} }

// NewMessageCommand builds a MESSAGE context-menu command (no description/options).
func NewMessageCommand(name string) *Command { return &Command{typ: cmdMessage, name: name} }

// Name returns the command's name.
func (c *Command) Name() string { return c.name }

// Loc adds a localized name and description for one locale.
func (c *Command) Loc(l Locale, name, description string) *Command {
	if c.nameLoc == nil {
		c.nameLoc, c.descLoc = map[string]string{}, map[string]string{}
	}
	c.nameLoc[string(l)] = name
	c.descLoc[string(l)] = description
	return c
}

// Perms sets default_member_permissions: members lacking every listed permission
// cannot see/use the command. With no args it clears to "0" (no one by default).
func (c *Command) Perms(perms ...Perm) *Command {
	var bits Perm
	for _, p := range perms {
		bits |= p
	}
	s := strconv.FormatUint(uint64(bits), 10)
	c.perms = &s
	return c
}

// DMPermission sets whether the command is usable in DMs.
func (c *Command) DMPermission(allowed bool) *Command { c.dmPerm = &allowed; return c }

// NSFW marks the command as age-restricted.
func (c *Command) NSFW() *Command { c.nsfw = true; return c }

// With appends options (plain options, Sub, or Group) to the command.
func (c *Command) With(opts ...Option) *Command {
	c.options = append(c.options, opts...)
	return c
}

// JSON returns the wire form Discord expects for this command.
func (c *Command) JSON() map[string]any { return c.build() }

func (c *Command) build() map[string]any {
	m := map[string]any{"name": c.name, "type": c.typ}
	if c.typ == cmdChatInput {
		m["description"] = c.desc
		if len(c.descLoc) > 0 {
			m["description_localizations"] = c.descLoc
		}
	}
	if len(c.nameLoc) > 0 {
		m["name_localizations"] = c.nameLoc
	}
	if c.perms != nil {
		m["default_member_permissions"] = *c.perms
	}
	if c.dmPerm != nil {
		m["dm_permission"] = *c.dmPerm
	}
	if c.nsfw {
		m["nsfw"] = true
	}
	if len(c.options) > 0 {
		m["options"] = buildOptions(c.options)
	}
	return m
}

// Option builds a command option, sub-command, or group.
type Option struct {
	typ          int
	name         string
	desc         string
	required     bool
	nameLoc      map[string]string
	descLoc      map[string]string
	choices      []Choice
	minVal       *float64
	maxVal       *float64
	minLen       *int
	maxLen       *int
	channelTypes []int
	autocomplete bool
	options      []Option
}

func leaf(typ int, name, desc string, required bool) Option {
	return Option{typ: typ, name: name, desc: desc, required: required}
}

func String(name, desc string, required bool) Option { return leaf(optString, name, desc, required) }
func Int(name, desc string, required bool) Option    { return leaf(optInteger, name, desc, required) }
func Bool(name, desc string, required bool) Option   { return leaf(optBoolean, name, desc, required) }
func User(name, desc string, required bool) Option   { return leaf(optUser, name, desc, required) }
func Mentionable(name, desc string, required bool) Option {
	return leaf(optMentionable, name, desc, required)
}
func Number(name, desc string, required bool) Option { return leaf(optNumber, name, desc, required) }

// ChannelOpt, RoleOpt and AttachmentOpt carry the -Opt suffix to avoid clashing
// with the Channel, Role and Attachment DTO types.
func ChannelOpt(name, desc string, required bool) Option {
	return leaf(optChannel, name, desc, required)
}
func RoleOpt(name, desc string, required bool) Option { return leaf(optRole, name, desc, required) }
func AttachmentOpt(name, desc string, required bool) Option {
	return leaf(optAttachment, name, desc, required)
}

// Sub builds a SUB_COMMAND carrying its own leaf options.
func Sub(name, desc string, opts ...Option) Option {
	return Option{typ: optSubCommand, name: name, desc: desc, options: opts}
}

// Group builds a SUB_COMMAND_GROUP carrying sub-commands.
func Group(name, desc string, subs ...Option) Option {
	return Option{typ: optSubCommandGroup, name: name, desc: desc, options: subs}
}

// Loc adds a localized name and description for one locale.
func (o Option) Loc(l Locale, name, desc string) Option {
	o.nameLoc = cloneSet(o.nameLoc, string(l), name)
	o.descLoc = cloneSet(o.descLoc, string(l), desc)
	return o
}

// Range sets min/max value (INTEGER or NUMBER options).
func (o Option) Range(min, max float64) Option { o.minVal, o.maxVal = &min, &max; return o }

// Len sets min/max length (STRING options).
func (o Option) Len(min, max int) Option { o.minLen, o.maxLen = &min, &max; return o }

// Choices sets the fixed choice list (STRING/INTEGER/NUMBER options).
func (o Option) Choices(cs ...Choice) Option { o.choices = cs; return o }

// ChannelTypes restricts a CHANNEL option to the given channel types.
func (o Option) ChannelTypes(types ...int) Option { o.channelTypes = types; return o }

// Autocomplete enables autocomplete for this option (mutually exclusive with Choices).
func (o Option) Autocomplete() Option { o.autocomplete = true; return o }

func (o Option) build() map[string]any {
	m := map[string]any{"type": o.typ, "name": o.name, "description": o.desc}
	if o.required {
		m["required"] = true
	}
	if len(o.nameLoc) > 0 {
		m["name_localizations"] = o.nameLoc
	}
	if len(o.descLoc) > 0 {
		m["description_localizations"] = o.descLoc
	}
	if len(o.choices) > 0 {
		cs := make([]map[string]any, 0, len(o.choices))
		for _, ch := range o.choices {
			cs = append(cs, ch.build())
		}
		m["choices"] = cs
	}
	if o.minVal != nil {
		m["min_value"] = *o.minVal
	}
	if o.maxVal != nil {
		m["max_value"] = *o.maxVal
	}
	if o.minLen != nil {
		m["min_length"] = *o.minLen
	}
	if o.maxLen != nil {
		m["max_length"] = *o.maxLen
	}
	if len(o.channelTypes) > 0 {
		m["channel_types"] = o.channelTypes
	}
	if o.autocomplete {
		m["autocomplete"] = true
	}
	if len(o.options) > 0 {
		m["options"] = buildOptions(o.options)
	}
	return m
}

func buildOptions(opts []Option) []map[string]any {
	out := make([]map[string]any, 0, len(opts))
	for _, o := range opts {
		out = append(out, o.build())
	}
	return out
}

// Choice is one fixed choice for a String/Int/Number option. Value must be a
// string, int, or float64 per the option type.
type Choice struct {
	name    string
	value   any
	nameLoc map[string]string
}

// NewChoice builds a fixed choice for a String/Int/Number option.
func NewChoice(name string, value any) Choice { return Choice{name: name, value: value} }

// Loc adds a localized display name for one locale.
func (c Choice) Loc(l Locale, name string) Choice {
	c.nameLoc = cloneSet(c.nameLoc, string(l), name)
	return c
}

func (c Choice) build() map[string]any {
	m := map[string]any{"name": c.name, "value": c.value}
	if len(c.nameLoc) > 0 {
		m["name_localizations"] = c.nameLoc
	}
	return m
}

func cloneSet(m map[string]string, key, val string) map[string]string {
	out := make(map[string]string, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	out[key] = val
	return out
}
