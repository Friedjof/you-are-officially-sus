package models

import "time"

// GameMode represents the type of game being played
type GameMode string

const (
GameModeStandard    GameMode = "standard"    // Auto-select word from places.json
GameModeCustomWords GameMode = "custom_words" // Players submit custom words
)

// Game represents an active game session (ephemeral)
type Game struct {
Mode            GameMode
Location        *Location
SpyID           string
SpyName         string                     // Store spy name in case they leave
FirstQuestioner string                     // Player ID of who asks the first question
PlayerInfo      map[string]*GamePlayerInfo // game-specific player data
Status          GameStatus
PlayStartedAt   time.Time // When the Playing phase started (for timer sync)

// Custom Words Mode fields
CustomWords        map[string]string // playerID -> submitted word
SelectedCustomWord string            // The randomly chosen word from CustomWords
WordsSubmitted     map[string]bool   // playerID -> has submitted word

ReadyToReveal    map[string]bool // Phase 1: Ready to see role (all players required)
ReadyAfterReveal map[string]bool // Phase 2: Confirmed saw role (all players required)
ReadyToVote      map[string]bool // Phase 3: Ready to vote (>50% required)
Votes            map[string]string
VoteRound        int  // Track voting rounds for tie-breaking
SpyForfeited     bool // True if spy left the game
}
