package main

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"dcbot/dca0"
	"dcbot/util"

	"github.com/bwmarrin/discordgo"
)

////////////////////////////////
// Helper functions.
////////////////////////////////
// The first return value contains the voice channel ID, if it was found. If it
// was not found, it is set to "".
// The second return value indicates whether the voice channel was found.
func GetUserVoiceChannel(g *discordgo.Guild, userID string) (string, bool) {
	for _, vs := range g.VoiceStates {
		if vs.UserID == userID {
			return vs.ChannelID, true
		}
	}
	return "", false
}

////////////////////////////////
// Structs.
////////////////////////////////
type Playback struct {
	Track
	CmdCh  chan dca0.Command
	RespCh chan dca0.Response
	Paused bool
	Loop   bool // Whether playback is looping right now.
}

type Track struct {
	Title    string // Title, if any.
	Url      string // Short URL, for example from YouTube.
	MediaUrl string // Long URL of the associated media file.
}

// All methods of Client are thread safe, however manual locking is required
// when accessing any fields.
type Client struct {
	sync.RWMutex

	// The discordgo session.
	s *discordgo.Session

	// TextChannelID and VoiceChannelID indicate the current channels through
	// which the bot should send text / audio. They may be set to "".
	TextChannelID  string
	VoiceChannelID string

	// Current audio playback.
	Playback *Playback
	// Queue.
	Queue []*Track
}

func NewClient(s *discordgo.Session) *Client {
	return &Client{
		s: s,
	}
}

func (c *Client) Messagef(format string, a ...interface{}) {
	c.RLock()
	if c.TextChannelID == "" {
		fmt.Printf(format+"\n", a...)
	} else {
		c.s.ChannelMessageSend(c.TextChannelID, fmt.Sprintf(format, a...))
	}
	c.RUnlock()
}

// Updates the text channel and voice channel IDs. May set them to "" if there
// are none associated with the message.
func (c *Client) UpdateChannels(g *discordgo.Guild, m *discordgo.Message) {
	c.Lock()
	c.TextChannelID = m.ChannelID

	vc, _ := GetUserVoiceChannel(g, m.Author.ID)
	c.VoiceChannelID = vc
	c.Unlock()
}

func (c *Client) GetTextChannelID() string {
	c.RLock()
	ret := c.TextChannelID
	c.RUnlock()
	return ret
}

func (c *Client) GetVoiceChannelID() string {
	c.RLock()
	ret := c.TextChannelID
	c.RUnlock()
	return ret
}

// Returns a COPY of c.Playback. Modifications to the returned Playback struct
// are NOT preserved, as it is a copy.
func (c *Client) GetPlaybackInfo() (p Playback, ok bool) {
	c.RLock()
	ret := c.Playback
	c.RUnlock()
	if ret == nil {
		return Playback{}, false
	} else {
		return *ret, true
	}
}

func (c *Client) QueueLen() int {
	c.RLock()
	l := len(c.Queue)
	c.RUnlock()
	return l
}

// Similarly to GetPlaybackInfo, this function returns a COPY. Any modifications
// are NOT preserved.
// ok field returns false if the index is out of bounds.
func (c *Client) QueueAt(i int) (t Track, ok bool) {
	l := c.QueueLen()
	if i >= l {
		return Track{}, false
	}
	c.RLock()
	ret := c.Queue[i]
	c.RUnlock()
	return *ret, true
}

func (c *Client) QueuePushBack(t *Track) {
	c.Lock()
	c.Queue = append(c.Queue, t)
	c.Unlock()
}

func (c *Client) QueuePushFront(t *Track) {
	c.Lock()
	c.Queue = append([]*Track{t}, c.Queue...)
	c.Unlock()
}

func (c *Client) QueuePopFront() (t Track, ok bool) {
	t, ok = c.QueueAt(0)
	if ok {
		c.Lock()
		c.Queue = c.Queue[1:]
		c.Unlock()
	}
	return t, ok
}

// Deletes a single item at any position.
// Returns false if i was out of bounds.
func (c *Client) QueueDelete(i int) bool {
	if i >= c.QueueLen() {
		return false
	}
	c.Lock()
	c.Queue = append(c.Queue[:i], c.Queue[i+1:]...)
	c.Unlock()
	return true
}

// Swaps two items in the queue.
// Returns false if a or b is out of bounds.
func (c *Client) QueueSwap(a, b int) bool {
	if a == b {
		return true
	}
	l := c.QueueLen()
	if a >= l || b >= l {
		return false
	}
	c.Lock()
	c.Queue[a], c.Queue[b] = c.Queue[b], c.Queue[a]
	c.Unlock()
	return true
}

func (c *Client) QueueClear() {
	c.Lock()
	c.Queue = nil
	c.Unlock()
}

func (c *Client) QueueFront() (t Track, ok bool) {
	c.Lock()
	defer c.Unlock()
	if len(c.Queue) == 0 {
		return Track{}, false
	}
	ret := *c.Queue[0]
	return ret, true
}

////////////////////////////////
// Global variables.
////////////////////////////////
var clients map[string]*Client // Guild ID to client
var mClients sync.Mutex

var cfg Config

////////////////////////////////
// Main program.
////////////////////////////////
func main() {
	if err := ReadConfig(&cfg); err != nil {
		fmt.Println(err)
		if err := WriteDefaultConfig(); err != nil {
			fmt.Println("Failed to create the default configuration file:", err)
			return
		}
		fmt.Println("Wrote the default configuration to " + configFile + ".")
		fmt.Println("You will have to manually configure the token by editing " + configFile + ".")
		return
	}

	if cfg.Token == tokenDefaultString {
		fmt.Println("Please set your bot token in " + configFile + " first.")
		return
	}

	// Check if all binary dependencies are installed correctly.
	const notInstalledErrMsg = "Unable to find %s in the specified path '%s', please make sure it's installed correctly.\nYou can manually set its path by editing %s\n"
	if !util.CheckInstalled(cfg.YtdlPath, "--version") {
		fmt.Printf(notInstalledErrMsg, "youtube-dl", cfg.YtdlPath, configFile)
		return
	}
	if !util.CheckInstalled(cfg.FfmpegPath, "-version") {
		fmt.Printf(notInstalledErrMsg, "ffmpeg", cfg.FfmpegPath, configFile)
		return
	}

	// Initialize client map.
	clients = make(map[string]*Client)

	// Initialize bot.
	dg, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		fmt.Println("Error creating Discord session:", err)
		return
	}

	dg.AddHandler(ready)
	dg.AddHandler(banAdd)
	dg.AddHandler(messageCreate)

	// What information we need about guilds.
	dg.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentsGuildVoiceStates | discordgo.IntentsGuildBans

	// Open the websocket and begin listening.
	err = dg.Open()
	if err != nil {
		fmt.Println("Error opening Discord session:", err)
		return
	}

	// Wait here until Ctrl+c or other term signal is received.
	fmt.Println("Bot is now running. Press Ctrl+c to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc

	fmt.Println("\nSignal received, closing Discord session.")

	// Cleanly close down the Discord session.
	dg.Close()
}

func ready(s *discordgo.Session, event *discordgo.Ready) {
	u := s.State.User
	fmt.Println("Logged in as", u.Username+"#"+u.Discriminator+".")
	s.UpdateListeningStatus(cfg.Prefix + "help")
}

func banAdd(s *discordgo.Session, event *discordgo.GuildBanAdd) {
	fmt.Println(event)
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore messages from the bot itself.
	if m.Author.ID == s.State.User.ID {
		return
	}

	var g *discordgo.Guild
	g, err := s.State.Guild(m.GuildID)
	if err != nil {
		// Could not find guild.
		s.ChannelMessageSend(m.ChannelID, "This bot only works in guilds (servers).")
		return
	}

	var c *Client
	mClients.Lock()
	{
		var ok bool
		if c, ok = clients[m.GuildID]; !ok {
			c = NewClient(s)
			clients[m.GuildID] = c
		}
	}
	mClients.Unlock()
	// Update the text and voice channels associated with the client.
	c.UpdateChannels(g, m.Message)

	args, ok := CmdGetArgs(m.Content)
	if !ok {
		// Not a command.
		return
	}

	if len(args) == 0 {
		c.Messagef("No command specified. Type `%shelp` for help.", cfg.Prefix)
		return
	}

	switch args[0] {
	case "help":
		commandHelp(c)
	case "play":
		commandPlay(s, g, c, args[1:])
	case "seek":
		commandSeek(c, args[1:])
	case "pos":
		commandPos(c)
	case "loop":
		commandLoop(c)
	case "add":
		commandAdd(c, args[1:], false)
	case "queue":
		commandQueue(c)
	case "pause":
		commandPause(c)
	case "stop":
		commandStop(c)
	case "skip":
		commandSkip(c)
	case "delete":
		commandDelete(c, args[1:])
	case "swap":
		commandSwap(c, args[1:])
	case "shuffle":
		commandShuffle(c)
	}
}
