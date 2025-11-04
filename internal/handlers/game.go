package handlers

import (
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aaronzipp/you-are-officially-sus/internal/game"
	"github.com/aaronzipp/you-are-officially-sus/internal/models"
	"github.com/aaronzipp/you-are-officially-sus/internal/render"
	"github.com/aaronzipp/you-are-officially-sus/internal/sse"
)

var debug bool

func init() {
	debug = os.Getenv("DEBUG") != ""
}

// HandleGameMux routes game subpaths by phase and actions
func (ctx *Context) HandleGameMux(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/game/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}
	roomCode := parts[0]
	seg := ""
	if len(parts) > 1 {
		seg = parts[1]
	}

// Reject unknown subpaths under /game/:code
if seg != "" && seg != "confirm-reveal" && seg != "roles" && seg != "play" && seg != "voting" && seg != "word-collection" && seg != "ready" && seg != "vote" && seg != "submit-word" && seg != "redirect" {
http.NotFound(w, r)
return
}

	// Redirect helper for HTMX
	if seg == "redirect" {
		to := r.URL.Query().Get("to")
		if to == "" {
			to = "/lobby/" + roomCode
		} else if !strings.HasPrefix(to, "/") {
			to = "/game/" + roomCode + "/" + to
		}
		w.Header().Set("HX-Location", to)
		w.WriteHeader(http.StatusOK)
		return
	}

// POST actions under /game/:code
if r.Method == http.MethodPost {
switch seg {
case "ready":
ctx.gameHandleReadyCookie(w, r, roomCode)
return
case "vote":
ctx.gameHandleVoteCookie(w, r, roomCode)
return
case "submit-word":
ctx.gameHandleSubmitWord(w, r, roomCode)
return
default:
http.Error(w, "Not found", http.StatusNotFound)
return
}
}

	// GET phase pages: confirm-reveal, roles, play, voting
	lobby, playerID, err := ctx.getLobbyAndPlayer(r, roomCode)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	lobby.RLock()
	g := lobby.CurrentGame
	lobby.RUnlock()
	if g == nil {
		http.Redirect(w, r, "/lobby/"+roomCode, http.StatusSeeOther)
		return
	}

	// Guard: ensure path matches current phase; redirect canonical path
	currentPath := game.PhasePathFor(roomCode, g.Status)
	if seg == "" || !strings.HasSuffix(currentPath, "/"+seg) {
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Redirect", currentPath)
			w.WriteHeader(http.StatusOK)
		} else {
			http.Redirect(w, r, currentPath, http.StatusSeeOther)
		}
		return
	}

	// Build page using per-phase template
	lobby.RLock()
	g = lobby.CurrentGame
	playerInfo := g.PlayerInfo[playerID]

	isReady := false
	switch g.Status {
	case models.StatusReadyCheck:
		isReady = g.ReadyToReveal[playerID]
	case models.StatusRoleReveal:
		isReady = g.ReadyAfterReveal[playerID]
	case models.StatusPlaying:
		isReady = g.ReadyToVote[playerID]
	}

	data := struct {
		RoomCode        string
		PlayerID        string
		Status          models.GameStatus
		Players         []*models.Player
		TotalPlayers    int
		Location        *models.Location
		Challenge       string
		IsSpy           bool
		IsReady         bool
		HasVoted        bool
		VoteRound       int
		FirstQuestioner string
		PlayStartedAt   int64 // Unix timestamp for client-side timer sync
		IsHost          bool
	}{
		RoomCode:        roomCode,
		PlayerID:        playerID,
		Status:          g.Status,
		Players:         render.GetPlayerList(lobby.Players),
		TotalPlayers:    len(lobby.Players),
		Location:        g.Location,
		Challenge:       playerInfo.Challenge,
		IsSpy:           playerInfo.IsSpy,
		IsReady:         isReady,
		HasVoted:        g.Votes[playerID] != "",
		VoteRound:       g.VoteRound,
		FirstQuestioner: g.FirstQuestioner,
		PlayStartedAt:   g.PlayStartedAt.Unix(),
		IsHost:          lobby.Host == playerID,
	}
	lobby.RUnlock()

// Handle word collection phase separately
if g.Status == models.StatusWordCollection {
ctx.handleWordCollectionPage(w, r, lobby, playerID, roomCode)
return
}

// Select template by phase
tmpl := ""
switch g.Status {
case models.StatusReadyCheck:
tmpl = "game_confirm_reveal.html"
case models.StatusRoleReveal:
tmpl = "game_roles.html"
case models.StatusPlaying:
tmpl = "game_play.html"
case models.StatusVoting:
tmpl = "game_voting.html"
default:
// Should not happen due to guard; send to lobby
w.Header().Set("HX-Redirect", "/lobby/"+roomCode)
w.WriteHeader(http.StatusOK)
return
}
ctx.Templates.ExecuteTemplate(w, tmpl, data)
}

// gameHandleReadyCookie updates readiness using cookie-based player ID
func (ctx *Context) gameHandleReadyCookie(w http.ResponseWriter, r *http.Request, roomCode string) {
	lobby, exists := ctx.LobbyStore.Get(roomCode)
	if !exists {
		http.Error(w, "Lobby not found", http.StatusNotFound)
		return
	}

	cookie, err := r.Cookie("player_id")
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	playerID := cookie.Value

	// Values derived from server state only
	var readyCountMsg string
	var buttonHTML string
	var shouldBroadcastPhase bool
	var readyCountEventName string

	lobby.Lock()
	g := lobby.CurrentGame
	if g == nil {
		lobby.Unlock()
		http.Error(w, "No game in progress", http.StatusBadRequest)
		return
	}

	statusBefore := g.Status

	// Update readiness per phase rules (toggle in all phases to surface issues)
	var isReady bool
	var prev bool
	var readyStateMap map[string]bool
	switch statusBefore {
	case models.StatusReadyCheck:
		readyStateMap = g.ReadyToReveal
		prev = g.ReadyToReveal[playerID]
		g.ReadyToReveal[playerID] = !g.ReadyToReveal[playerID]
		isReady = g.ReadyToReveal[playerID]
	case models.StatusRoleReveal:
		readyStateMap = g.ReadyAfterReveal
		prev = g.ReadyAfterReveal[playerID]
		g.ReadyAfterReveal[playerID] = !g.ReadyAfterReveal[playerID]
		isReady = g.ReadyAfterReveal[playerID]
	case models.StatusPlaying:
		readyStateMap = g.ReadyToVote
		prev = g.ReadyToVote[playerID]
		g.ReadyToVote[playerID] = !g.ReadyToVote[playerID]
		isReady = g.ReadyToVote[playerID]
	default:
		lobby.Unlock()
		http.Error(w, "Invalid game phase", http.StatusBadRequest)
		return
	}

	// Compute ready count from server state (no client math) and gather confirmed names using lobby players
	readyCount := 0
	confirmedNames := make([]string, 0)
	for id := range lobby.Players {
		if readyStateMap[id] {
			readyCount++
			if p, ok := lobby.Players[id]; ok {
				confirmedNames = append(confirmedNames, p.Name)
			} else {
				confirmedNames = append(confirmedNames, "unknown("+id+")")
			}
		}
	}
	totalPlayers := len(lobby.Players)

	// Actor name for logging
	actorName := "unknown"
	if p, ok := lobby.Players[playerID]; ok {
		actorName = p.Name
	}

	// Decide whether to advance based on the computed count
	shouldAdvance := false
	switch statusBefore {
	case models.StatusReadyCheck, models.StatusRoleReveal:
		shouldAdvance = readyCount == totalPlayers
	case models.StatusPlaying:
		shouldAdvance = readyCount > totalPlayers/2
	}

	// Prepare outgoing UI for the CURRENT (pre-advance) phase
	switch statusBefore {
	case models.StatusReadyCheck:
		readyCountMsg = ctx.ReadyCount(readyCount, len(lobby.Players), "players ready")
		readyCountEventName = "ready-count-check"
	case models.StatusRoleReveal:
		readyCountMsg = ctx.ReadyCount(readyCount, len(lobby.Players), "players ready")
		readyCountEventName = "ready-count-reveal"
	case models.StatusPlaying:
		readyCountMsg = ctx.ReadyCount(readyCount, len(lobby.Players), "players ready to vote")
		readyCountEventName = "ready-count-playing"
	}

	buttonID := "ready-button-check"
	buttonText := "I'm Ready to See My Role"
	buttonClass := "btn btn-primary"
	switch statusBefore {
	case models.StatusReadyCheck:
		buttonID = "ready-button-check"
		if isReady {
			buttonText = "✓ Ready - Waiting for others..."
			buttonClass = "btn btn-success"
		} else {
			buttonText = "I'm Ready to See My Role"
			buttonClass = "btn btn-primary"
		}
	case models.StatusRoleReveal:
		buttonID = "ready-button-role"
		if isReady {
			buttonText = "✓ Waiting for others..."
			buttonClass = "btn btn-success"
		} else {
			buttonText = "I've Seen My Role ✓"
			buttonClass = "btn btn-primary"
		}
	case models.StatusPlaying:
		buttonID = "ready-button-playing"
		if isReady {
			buttonText = "✓ Ready to Vote"
			buttonClass = "btn btn-success"
		} else {
			buttonText = "Ready to Vote?"
			buttonClass = "btn btn-secondary"
		}
	}
	var bb strings.Builder
	bb.WriteString(`<button id="`)
	bb.WriteString(buttonID)
	bb.WriteString(`" type="submit" class="`)
	bb.WriteString(buttonClass)
	bb.WriteString(`">`)
	bb.WriteString(buttonText)
	bb.WriteString(`</button>`)
	buttonHTML = bb.String()

	// Detailed logging for readiness change
	if debug {
		log.Printf("ready: room=%s phase=%s actor=%s(%s) prev=%v now=%v confirmed=[%s] count=%d/%d", roomCode, statusBefore, actorName, playerID, prev, isReady, strings.Join(confirmedNames, ", "), readyCount, totalPlayers)
	}

	// Advance AFTER preparing current-phase outputs
	nextPath := ""
	if shouldAdvance {
		switch statusBefore {
		case models.StatusReadyCheck:
			g.Status = models.StatusRoleReveal
			// Pre-seed next phase readiness map
			for id := range lobby.Players {
				if _, ok := g.ReadyAfterReveal[id]; !ok {
					g.ReadyAfterReveal[id] = false
				}
			}
			nextPath = game.PhasePathFor(roomCode, g.Status)
			shouldBroadcastPhase = true
		case models.StatusRoleReveal:
			g.Status = models.StatusPlaying
			// Record when playing phase started (for timer sync)
			g.PlayStartedAt = time.Now()
			// Pre-seed next phase readiness map
			for id := range lobby.Players {
				if _, ok := g.ReadyToVote[id]; !ok {
					g.ReadyToVote[id] = false
				}
			}
			// Choose random first questioner
			playerIDs := make([]string, 0, len(lobby.Players))
			for id := range lobby.Players {
				playerIDs = append(playerIDs, id)
			}
			g.FirstQuestioner = playerIDs[rand.Intn(len(playerIDs))]
			nextPath = game.PhasePathFor(roomCode, g.Status)
			shouldBroadcastPhase = true
		case models.StatusPlaying:
			g.Status = models.StatusVoting
			nextPath = game.PhasePathFor(roomCode, g.Status)
			shouldBroadcastPhase = true
		}
	}
	lobby.Unlock()

	// Broadcast the server-derived current-phase count
	sse.Broadcast(lobby, readyCountEventName, readyCountMsg)

	// If phase advanced, instruct clients to navigate; no client-side math
	if shouldBroadcastPhase {
		sse.Broadcast(lobby, sse.EventNavRedirect, ctx.RedirectSnippet(roomCode, nextPath))
		// Also ensure the initiating client navigates via HX-Redirect
		w.Header().Set("HX-Redirect", nextPath)
		w.WriteHeader(http.StatusOK)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(buttonHTML))
}

// gameHandleVoteCookie records a vote using cookie-based player ID
func (ctx *Context) gameHandleVoteCookie(w http.ResponseWriter, r *http.Request, roomCode string) {
	lobby, exists := ctx.LobbyStore.Get(roomCode)
	if !exists {
		http.Error(w, "Lobby not found", http.StatusNotFound)
		return
	}

	cookie, err := r.Cookie("player_id")
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	playerID := cookie.Value

	r.ParseForm()
	suspectID := r.FormValue("suspect")

	var voteCountMsg string
	var shouldFinish bool
	var shouldRevote bool
	var scoresUpdated bool

	lobby.Lock()
	g := lobby.CurrentGame
	if g == nil || g.Status != models.StatusVoting {
		lobby.Unlock()
		http.Error(w, "Not in voting phase", http.StatusBadRequest)
		return
	}

	g.Votes[playerID] = suspectID

	if len(g.Votes) == len(lobby.Players) {
		// Count votes
		voteCount := make(map[string]int)
		for _, votedFor := range g.Votes {
			voteCount[votedFor]++
		}

		maxVotes := 0
		var playersWithMaxVotes []string
		for pID, count := range voteCount {
			if count > maxVotes {
				maxVotes = count
				playersWithMaxVotes = []string{pID}
			} else if count == maxVotes {
				playersWithMaxVotes = append(playersWithMaxVotes, pID)
			}
		}

		if len(playersWithMaxVotes) > 1 && g.VoteRound < game.MaxVoteRounds {
			// tie -> revote
			g.Votes = make(map[string]string)
			g.VoteRound++
			shouldRevote = true
		} else {
			// finish game
			g.Status = models.StatusFinished
			innocentWon := len(playersWithMaxVotes) == 1 && playersWithMaxVotes[0] == g.SpyID
			for id := range lobby.Players {
				if id == g.SpyID {
					if innocentWon {
						lobby.Scores[id].GamesLost++
					} else {
						lobby.Scores[id].GamesWon++
					}
				} else {
					if innocentWon {
						lobby.Scores[id].GamesWon++
					} else {
						lobby.Scores[id].GamesLost++
					}
				}
			}
			shouldFinish = true
			scoresUpdated = true
		}
	}

	voteCountMsg = ctx.VoteCount(len(g.Votes), len(lobby.Players))
	lobby.Unlock()

	sse.Broadcast(lobby, sse.EventVoteCount, voteCountMsg)
	if scoresUpdated {
		sse.Broadcast(lobby, sse.EventPlayerUpdate, ctx.PlayerList(lobby.Players, lobby.Scores, lobby.Host))
	}
	if shouldRevote {
		sse.Broadcast(lobby, sse.EventNavRedirect, ctx.RedirectSnippet(roomCode, game.PhasePathFor(roomCode, models.StatusVoting)))
	} else if shouldFinish {
		sse.Broadcast(lobby, sse.EventNavRedirect, ctx.RedirectSnippet(roomCode, game.PhasePathFor(roomCode, models.StatusFinished)))
	}

w.Header().Set("Content-Type", "text/html")
w.Write([]byte(ctx.VotedConfirmation()))
}

// handleWordCollectionPage renders the word collection page
func (ctx *Context) handleWordCollectionPage(w http.ResponseWriter, r *http.Request, lobby *models.Lobby, playerID, roomCode string) {
lobby.RLock()
g := lobby.CurrentGame

// Count words submitted
wordsSubmittedCount := 0
for _, submitted := range g.WordsSubmitted {
if submitted {
wordsSubmittedCount++
}
}

// Check if player has submitted a word
hasSubmittedWord := g.WordsSubmitted[playerID]
submittedWord := ""
if hasSubmittedWord {
submittedWord = g.CustomWords[playerID]
}

data := struct {
RoomCode            string
PlayerID            string
Players             []*models.Player
TotalPlayers        int
HasSubmittedWord    bool
SubmittedWord       string
WordsSubmittedCount int
WordsSubmitted      map[string]bool
IsHost              bool
}{
RoomCode:            roomCode,
PlayerID:            playerID,
Players:             render.GetPlayerList(lobby.Players),
TotalPlayers:        len(lobby.Players),
HasSubmittedWord:    hasSubmittedWord,
SubmittedWord:       submittedWord,
WordsSubmittedCount: wordsSubmittedCount,
WordsSubmitted:      g.WordsSubmitted,
IsHost:              lobby.Host == playerID,
}
lobby.RUnlock()

ctx.Templates.ExecuteTemplate(w, "game_word_collection.html", data)
}

// gameHandleSubmitWord handles word submission in custom words mode
func (ctx *Context) gameHandleSubmitWord(w http.ResponseWriter, r *http.Request, roomCode string) {
lobby, exists := ctx.LobbyStore.Get(roomCode)
if !exists {
http.Error(w, "Lobby not found", http.StatusNotFound)
return
}

cookie, err := r.Cookie("player_id")
if err != nil {
http.Error(w, "Unauthorized", http.StatusUnauthorized)
return
}
playerID := cookie.Value

r.ParseForm()
word := strings.TrimSpace(r.FormValue("word"))
if word == "" {
http.Error(w, "Word is required", http.StatusBadRequest)
return
}

// Sanitize word (limit length and clean up)
if len(word) > 50 {
word = word[:50]
}

var wordCountMsg string
var shouldAdvance bool

lobby.Lock()
g := lobby.CurrentGame
if g == nil || g.Status != models.StatusWordCollection {
lobby.Unlock()
http.Error(w, "Not in word collection phase", http.StatusBadRequest)
return
}

// Check if player already submitted
if g.WordsSubmitted[playerID] {
lobby.Unlock()
http.Error(w, "Word already submitted", http.StatusBadRequest)
return
}

// Store the word
g.CustomWords[playerID] = word
g.WordsSubmitted[playerID] = true

// Count submitted words
wordsSubmittedCount := 0
for _, submitted := range g.WordsSubmitted {
if submitted {
wordsSubmittedCount++
}
}
totalPlayers := len(lobby.Players)

// Check if all words are submitted
if wordsSubmittedCount == totalPlayers {
// All words collected, now assign spy and select word
ctx.assignSpyAndSelectWord(g, lobby.Players)

// Advance to ready check phase
g.Status = models.StatusReadyCheck
// Pre-seed readiness map
for id := range lobby.Players {
g.ReadyToReveal[id] = false
}
shouldAdvance = true
}

wordCountMsg = ctx.WordCollectionCount(wordsSubmittedCount, totalPlayers)
lobby.Unlock()

// Broadcast word collection count update
sse.Broadcast(lobby, "word-collection-count", wordCountMsg)

if shouldAdvance {
// All words collected, advance to next phase
nextPath := game.PhasePathFor(roomCode, models.StatusReadyCheck)
sse.Broadcast(lobby, sse.EventNavRedirect, ctx.RedirectSnippet(roomCode, nextPath))
w.Header().Set("HX-Redirect", nextPath)
w.WriteHeader(http.StatusOK)
return
}

// Return success message for individual word submission
w.Header().Set("Content-Type", "text/html")
w.Write([]byte(`<div class="text-center">
<h2>✓ Word Submitted!</h2>
<p>You submitted: <strong>"` + word + `"</strong></p>
<p class="text-muted">Waiting for other players to submit their words...</p>
</div>`))
}

// assignSpyAndSelectWord assigns a random spy and selects a random word from submissions
func (ctx *Context) assignSpyAndSelectWord(g *models.Game, players map[string]*models.Player) {
// Create list of all words
words := make([]string, 0, len(g.CustomWords))
for _, word := range g.CustomWords {
words = append(words, word)
}

// Select random word
selectedWord := words[rand.Intn(len(words))]
g.SelectedCustomWord = selectedWord

// Create location object for the selected word
g.Location = &models.Location{
Word:       selectedWord,
Categories: []string{"custom"},
}

// Create list of player IDs
playerIDs := make([]string, 0, len(players))
for id := range players {
playerIDs = append(playerIDs, id)
}

// Assign random spy
spyID := playerIDs[rand.Intn(len(playerIDs))]
g.SpyID = spyID
g.SpyName = players[spyID].Name

// Assign challenges and roles
shuffledChallenges := make([]string, len(ctx.Challenges))
copy(shuffledChallenges, ctx.Challenges)
rand.Shuffle(len(shuffledChallenges), func(i, j int) {
shuffledChallenges[i], shuffledChallenges[j] = shuffledChallenges[j], shuffledChallenges[i]
})

for i, id := range playerIDs {
g.PlayerInfo[id] = &models.GamePlayerInfo{
Challenge: shuffledChallenges[i%len(shuffledChallenges)],
IsSpy:     id == g.SpyID,
}
}

log.Printf("Custom words game: selected word='%s' spy=%s(%s)", selectedWord, g.SpyName, spyID)
}
