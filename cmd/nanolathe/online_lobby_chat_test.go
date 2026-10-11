package main

import (
	"errors"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/relay"
)

// onlineChatTestRoom opens a host's lobby with three other players: Ann and
// an unnamed player whose clients carry chat, and one on an older protocol
// whose client does not. It returns the client whose input drives the lobby.
func onlineChatTestRoom(t *testing.T, g *gameShell) (*fakeOnlineLobby, *client.Client) {
	t.Helper()
	useOnlineSessionSeams(t, 4)
	fake := newFakeOnlineLobby(0, 0, 1, 2, 3)
	fake.state.Seats[0].Name, fake.state.Seats[0].Chat = "Daniel", true
	fake.state.Seats[1].Name, fake.state.Seats[1].Chat = "Ann", true
	fake.state.Seats[2].Chat = true
	fake.state.Seats[3].Name = "Ghost"
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	saved := clPtr
	clPtr = cl
	t.Cleanup(func() { clPtr = saved })
	openTestLobby(t, g, fake, onlineTestCatalog())
	return fake, cl
}

// chatKeys feeds tokens to the lobby one shell step each, as the window does.
func chatKeys(g *gameShell, cl *client.Client, tokens ...input.Token) {
	for _, token := range tokens {
		cl.Input().EnqueueToken(token)
		g.pollOnline()
		g.menuInput(cl)
	}
	g.pollOnline()
}

func chatText(text string) []input.Token {
	var tokens []input.Token
	for _, r := range text {
		tokens = append(tokens, input.Token{Kind: input.TokenText, Rune: r})
	}
	return tokens
}

var (
	chatEnter  = input.Token{Kind: input.TokenEdit, Key: input.KeyEnter}
	chatEscape = input.Token{Kind: input.TokenEdit, Key: input.KeyEscape}
)

// Enter opens the text line and Enter sends it, as the bare text, to every
// other present seat whose client carries chat; the sender sees its own line
// as "<name> text" and received lines name their senders as the lobby does.
// A refused line is said in plain words, and Escape drops the line without
// leaving the room (DESIGN_MULTIPLAYER §16.6.2).
func TestOnlineLobbyChatRouting(t *testing.T) {
	g := onlineTestShell(t)
	fake, cl := onlineChatTestRoom(t, g)
	r := &g.online.room
	p := r.panel
	if !p.ActiveOf(lobbyChatLine) || lobbyText(g, "Player3") != "Player 4" {
		t.Fatalf("chat offered %v, the older client's row %q", p.ActiveOf(lobbyChatLine), lobbyText(g, "Player3"))
	}
	chatKeys(g, cl, chatEnter)
	if !r.chat.editing || !p.EditorCaptured() || p.EditorIndex() != p.Index(lobbyChatLine) {
		t.Fatal("Enter did not open the chat line")
	}
	chatKeys(g, cl, append(chatText("gl hf"), chatEnter)...)
	if len(fake.chats) != 1 || fake.chats[0] != (fakeChatLine{recipients: 1<<1 | 1<<2, text: "gl hf"}) {
		t.Fatalf("sent %+v", fake.chats)
	}
	if r.chat.editing || p.EditorCaptured() || p.TextOf(lobbyChatLine) != "" || r.chat.lines[len(r.chat.lines)-1] != "<Daniel> gl hf" {
		t.Fatalf("after sending: editing %v, line %q, log %q", r.chat.editing, p.TextOf(lobbyChatLine), r.chat.lines)
	}
	fake.inbox = []relay.HostedChatLine{{Seat: 1, Text: "you too"}, {Seat: 2, Text: "ready?"}}
	g.pollOnline()
	if n := len(r.chat.lines); n != 3 || r.chat.lines[1] != "<Ann> you too" || r.chat.lines[2] != "<Player 3> ready?" {
		t.Fatalf("received %q", r.chat.lines)
	}
	// Beyond the rate the line stays open with its text, and says why.
	fake.chatErr = relay.ErrHostedChatRate
	chatKeys(g, cl, chatEnter)
	chatKeys(g, cl, append(chatText("wait"), chatEnter)...)
	if !r.chat.editing || p.TextOf(lobbyChatLine) != "wait" || r.chat.lines[len(r.chat.lines)-1] != onlineChatRateText {
		t.Fatalf("rate refusal: editing %v, line %q, log %q", r.chat.editing, p.TextOf(lobbyChatLine), r.chat.lines)
	}
	chatKeys(g, cl, chatEnter)
	if len(fake.chats) != 2 || fake.chats[1].text != "wait" {
		t.Fatalf("the line was not sent again: %+v", fake.chats)
	}
	// Another refusal closes the line with its reason.
	fake.chatErr = errors.New("nanolathe: hosted relay rejected: logical path lobby, providers searched [hosted transport], expected an open lobby")
	chatKeys(g, cl, append(append([]input.Token{chatEnter}, chatText("bye")...), chatEnter)...)
	if r.chat.editing || r.chat.lines[len(r.chat.lines)-1] != "Not sent: You left the game." {
		t.Fatalf("other refusal: %q", r.chat.lines[len(r.chat.lines)-1])
	}
	// Escape drops the line and stays in the room.
	chatKeys(g, cl, append(append([]input.Token{chatEnter}, chatText("never mind")...), chatEscape)...)
	if g.online == nil || fake.closes != 0 || r.chat.editing || p.TextOf(lobbyChatLine) != "" || p.Window.Header.EscDefault != "PrevMenu" || len(fake.chats) != 2 {
		t.Fatal("Escape did not drop the chat line, or left the room")
	}
	// A ready player still chats.
	r.prepared = &onlinePrepared{key: r.key()}
	g.activateGadget(lobbyReady)
	chatKeys(g, cl, append(append([]input.Token{chatEnter}, chatText("ready")...), chatEnter)...)
	if !r.ready || len(fake.chats) != 3 || fake.chats[2].text != "ready" {
		t.Fatalf("a ready player's chat: %+v", fake.chats)
	}
	// With only the older client left beside the host, chat is not offered.
	for _, seat := range []int{1, 2} {
		fake.state.Seats[seat] = relay.HostedSeatState{}
	}
	g.pollOnline()
	chatKeys(g, cl, chatEnter)
	if p.ActiveOf(lobbyChatLine) || r.chat.editing {
		t.Fatal("chat is offered with no one to read it")
	}
}

// A right press anywhere drops the open line, as Escape does, and does
// nothing else: a right click on the player's own colour does not step it
// back until the button is free again.
func TestOnlineLobbyChatRightClickDrops(t *testing.T) {
	g := onlineTestShell(t)
	fake, cl := onlineChatTestRoom(t, g)
	r := &g.online.room
	p := r.panel
	colour := p.Window.PlacedRect(p.Index("Color0"))
	mouse := cl.Input().Mouse
	rightClick := func() {
		mouse.SetPosition(float32(colour.X+colour.W/2), float32(colour.Y+colour.H/2))
		mouse.SetButton(input.MouseButtonRight, true)
		g.pollOnline()
		g.menuInput(cl)
		mouse.ResetEdges()
		mouse.SetButton(input.MouseButtonRight, false)
		g.pollOnline()
		g.menuInput(cl)
		mouse.ResetEdges()
		g.pollOnline()
	}
	chatKeys(g, cl, append([]input.Token{chatEnter}, chatText("oops")...)...)
	if !r.chat.editing {
		t.Fatal("the line did not open")
	}
	rightClick()
	if r.chat.editing || p.EditorCaptured() || p.TextOf(lobbyChatLine) != "" || len(fake.chats) != 0 || len(fake.colors) != 0 || r.chat.swallowRight {
		t.Fatalf("right press: editing %v, line %q, colours %v", r.chat.editing, p.TextOf(lobbyChatLine), fake.colors)
	}
	rightClick()
	if len(fake.colors) != 1 {
		t.Fatalf("the next right click on the colour did nothing: %v", fake.colors)
	}
}
