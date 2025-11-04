package handlers

import (
	"bytes"
	"html/template"
	"log"
	"net/http"

	"github.com/aaronzipp/you-are-officially-sus/internal/models"
	"github.com/aaronzipp/you-are-officially-sus/internal/render"
	"github.com/aaronzipp/you-are-officially-sus/internal/store"
)

// Context holds shared application dependencies
type Context struct {
	LobbyStore *store.LobbyStore
	Templates  *template.Template
	Locations  []models.Location
	Challenges []string
	BaseURL    string
}

// ExecutePartial executes a template partial and returns the HTML string
func (ctx *Context) ExecutePartial(name string, data interface{}) string {
	var buf bytes.Buffer
	if err := ctx.Templates.ExecuteTemplate(&buf, name, data); err != nil {
		// Log error to help debug template issues
		log.Printf("ERROR: ExecutePartial failed for %s: %v (data type: %T)", name, err, data)
		return ""
	}
	return buf.String()
}

type playerListViewData struct {
	Players    []*models.Player
	Scores     map[string]*models.PlayerScore
	HasResults bool
	HostID     string
}

func (ctx *Context) buildPlayerListData(players map[string]*models.Player, scores map[string]*models.PlayerScore, hostID string) playerListViewData {
	hasResults := false
	for _, score := range scores {
		if score == nil {
			continue
		}
		if score.GamesWon > 0 || score.GamesLost > 0 {
			hasResults = true
			break
		}
	}

	var orderedPlayers []*models.Player
	if hasResults {
		orderedPlayers = render.GetPlayerListSortedByScore(players, scores)
	} else {
		orderedPlayers = render.GetPlayerList(players)
	}

	return playerListViewData{
		Players:    orderedPlayers,
		Scores:     scores,
		HasResults: hasResults,
		HostID:     hostID,
	}
}

// PlayerList generates HTML for the player list using template partials
func (ctx *Context) PlayerList(players map[string]*models.Player, scores map[string]*models.PlayerScore, hostID string) string {
	data := ctx.buildPlayerListData(players, scores, hostID)
	return ctx.ExecutePartial("player_list.html", data)
}

// HostControls generates HTML for host controls using template partials
func (ctx *Context) HostControls(lobby *models.Lobby, playerID string) string {
	hostName := ""
	if host, ok := lobby.Players[lobby.Host]; ok && host != nil {
		hostName = host.Name
	}
	return ctx.ExecutePartial("host_controls.html", struct {
		IsHost      bool
		PlayerCount int
		InGame      bool
		RoomCode    string
		HostName    string
	}{
		IsHost:      lobby.Host == playerID,
		PlayerCount: len(lobby.Players),
		InGame:      lobby.CurrentGame != nil,
		RoomCode:    lobby.Code,
		HostName:    hostName,
	})
}

// ReadyCount generates HTML for ready count display
func (ctx *Context) ReadyCount(ready, total int, label string) string {
	return ctx.ExecutePartial("ready_count.html", struct {
		ReadyCount int
		TotalCount int
		Label      string
	}{
		ReadyCount: ready,
		TotalCount: total,
		Label:      label,
	})
}

// VoteCount generates HTML for vote count display
func (ctx *Context) VoteCount(count, total int) string {
return ctx.ExecutePartial("vote_count.html", struct {
VoteCount  int
TotalCount int
}{
VoteCount:  count,
TotalCount: total,
})
}

// WordCollectionCount generates HTML for word collection count display
func (ctx *Context) WordCollectionCount(submitted, total int) string {
return ctx.ExecutePartial("ready_count.html", struct {
ReadyCount int
TotalCount int
Label      string
}{
ReadyCount: submitted,
TotalCount: total,
Label:      "players have submitted words",
})
}

// VotedConfirmation generates HTML for "you voted" confirmation
func (ctx *Context) VotedConfirmation() string {
	return ctx.ExecutePartial("voted_confirmation.html", nil)
}

// ErrorMessage generates HTML for error messages
func (ctx *Context) ErrorMessage(message string) string {
	return ctx.ExecutePartial("error_message.html", struct {
		Message string
	}{
		Message: message,
	})
}

// RedirectSnippet returns an HTMX snippet that triggers a client-side redirect
func (ctx *Context) RedirectSnippet(roomCode, to string) string {
	return ctx.ExecutePartial("redirect_snippet.html", struct {
		RoomCode string
		To       string
	}{
		RoomCode: roomCode,
		To:       to,
	})
}

// GameAbortedMessage generates HTML for game aborted warning
func (ctx *Context) GameAbortedMessage(reason string) string {
	return ctx.ExecutePartial("game_aborted_message.html", struct {
		Reason string
	}{
		Reason: reason,
	})
}

// HostNotification generates HTML for new host notification
func (ctx *Context) HostNotification() string {
	return ctx.ExecutePartial("host_notification.html", nil)
}

// HandleIndex serves the landing page
func (ctx *Context) HandleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	ctx.Templates.ExecuteTemplate(w, "index.html", nil)
}
