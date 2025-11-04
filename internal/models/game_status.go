package models

// GameStatus represents the current state of the game
type GameStatus string

const (
StatusWaiting       GameStatus = "waiting"
StatusWordCollection GameStatus = "word_collection"
StatusReadyCheck    GameStatus = "ready_check"
StatusRoleReveal    GameStatus = "role_reveal"
StatusPlaying       GameStatus = "playing"
StatusVoting        GameStatus = "voting"
StatusFinished      GameStatus = "finished"
)
