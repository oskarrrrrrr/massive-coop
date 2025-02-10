# Massive Co-Op Tic-Tac-Toe

In this game, each player is automatically assigned to either team O or
team X. During their turn, players can vote for any legal move, and once
the voting period ends, the move with the most votes is played. If there
are no players or no votes, a random move will be made. Grey numbers indicate
the vote counts, which are either shown after a move is made or continuously
during the opponent's turn.

To test the game for multiple players you can open the game in multiple tabs.

## Running

Run `go mod tidy` and `go run main.go` then go to `localhost:8080`.
