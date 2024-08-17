const WS_URL = wsUrl("/api/ws")
let socket = connectSocket(WS_URL)
const MAX_RETRIES = 5
const INITIAL_TIMEOUT = 1000
let retryTimeoutMs = INITIAL_TIMEOUT
let retries = 0

let currentTeam = ""
let token = ""
let team = ""
let timerEndTime = null

function wsUrl(relativePath) {
    const urlPrefix = (window.location.protocol === "https:") ? "wss://" : "ws://"
    return urlPrefix + window.location.host + relativePath
}

function connectSocket(url) {
    console.log(`Establishing websocket connection with ${url}.`)
    let socket = new WebSocket(url);

    socket.onopen = () => {
        console.log("Connected")
        retries = 0
        retryTimeoutMs = INITIAL_TIMEOUT
    }

    socket.onclose = (event) => {
        console.log("Socket closed conn: ", event)
        socket.send("Client closed")
    }

    socket.onerror = (error) => {
        // TODO: print err message to the user
        console.log("Socket error: ", error)
        retry(url)
    }

    socket.onmessage = (event) => {
        const msg = event.data
        const lines = msg.split("\n")
        if (lines[0] != "PlayerCount" && lines[0] != "VoteCounts") {
            console.log(`Got msg: ${msg}`)
        }
        switch (lines[0]) {
            case "GameState":
                const cells = lines[1].slice(1,-1).split(" ")
                for (let row = 0; row < 3; row++) {
                    for (let col = 0; col < 3; col++) {
                        const i  = row * 3 + col
                        if (cells[i] == "1") {
                            setCell("O")
                        } else if (cells[i] == "2") {
                            setCell("X")
                        } else {
                            setCell("")
                        }
                    }
                }
                break
            case "GameOver":
                currentTeam = ""
                clearCellSelections()
                const result = lines[1]
                if (result == "Draw") {
                    setMessageBox("Game Over! Draw.")
                } else if (result == team) {
                    setMessageBox("Game Over! You Win!")
                } else {
                    setMessageBox("Game Over! You Lose!")
                }
                break
            case "Board":
                const values = lines[1].split(",")
                for (let row = 0; row < 3; row++) {
                    for (let col = 0; col < 3; col++) {
                        let i = row * 3 + col
                        setCell(row, col, values[i])
                    }
                }
                break
            case "PlayerCount":
                setOPlayersCount(parseInt(lines[1]))
                setXPlayersCount(parseInt(lines[2]))
                break;
            case "Round":
                clearCellSelections()
                token = lines[1]
                timerEndTime = Date.parse(lines[2])
                currentTeam = lines[3]
                if (currentTeam == team) {
                    setMessageBox("Your turn!")
                } else {
                    setMessageBox("Opponent's turn.")
                }
                break
            case "Move":
                let [row, col] = lines[1].split(",")
                setCell(row, col, lines[2])
                break
            case "VoteCounts":
                const votes = lines[2].split(",")
                let i = 0
                for (let row = 0; row < 3; row++) {
                    for (let col = 0; col < 3; col++) {
                        if (votes[i] == "") {
                            clearVoteCount(row, col)
                        } else {
                            setVoteCount(row, col, votes[i])
                        }
                        i++;
                    }
                }
                break
            case "Team":
                team = lines[1]
                setTeam(team)
                break
        }
    }

    return socket
}

function retry(url) {
    if (retries >= MAX_RETRIES) return
    console.log(`setting retry in ${retryTimeoutMs}ms`)
    setTimeout(
        () => { socket = connectSocket(url) },
        retryTimeoutMs,
    )
    retries++
    retryTimeoutMs *= 2
}

function cellOnClick(row, col) {
    if (currentTeam != team || getCellVal(row, col) != "" || anyCellSelected) {
        return
    }
    selectCell(row, col)
    const msg = `Vote\n${token}\n${row},${col}`
    console.log(`Sending message: ${msg}`)
    socket.send(msg)
}

let anyCellSelected = false

function selectCell(row, col) {
    if (anyCellSelected) return
    let cell = getCellWrapper(row, col)
    cell.classList.add("tttCellSelected")
    anyCellSelected = true
}

function clearCellSelections() {
    for (let row = 0; row < 3; row++) {
        for (let col = 0; col < 3; col++) {
            getCellWrapper(row, col).classList.remove("tttCellSelected")
        }
    }
    anyCellSelected = false
}

function getCellWrapper(row, col) {
    return document.getElementById(`tttCellWrapper-${row}-${col}`)
}

function getCell(row, col) {
    return document.getElementById(`tttCell-${row}-${col}`)
}

function getCellVal(row, col) {
    return getCell(row, col).innerText
}

function setCell(row, col, val) {
    document.getElementById(`tttCell-${row}-${col}`).innerText = val
}

function setMessageBox(msg) {
    document.getElementById("message-box").textContent = msg
}

function setTeam(team) {
    document.getElementById("your-team").textContent = team
}

function setTimeLeft(time) {
    document.getElementById("time-left").textContent = time
}

function setOPlayersCount(num) {
    document.getElementById("o-players-count").textContent = num
}

function setXPlayersCount(num) {
    document.getElementById("x-players-count").textContent = num
}

function getVoteDiv(row, col) {
    return document.getElementById(`voteCounter-${row}-${col}`)
}

function setVoteCount(row, col, count) {
    getVoteDiv(row, col).textContent = count
}

function clearVoteCount(row, col) {
    getVoteDiv(row, col).textContent = ""
}

function hideVoteCount(row, col) {
    getVoteDiv(row, col).textContent = ""
}

for (let row = 0; row < 3; row++) {
    for (let col = 0; col < 3; col++) {
        const cellWrapper = getCellWrapper(row, col)
        cellWrapper.onclick = () => cellOnClick(row, col)
        setCell(row, col, "")
    }
}

function updateTimer() {
    if (timerEndTime == null) {
        return
    }
    const diff = timerEndTime - new Date().getTime()
    if (diff.valueOf() < 0) {
        setTimeLeft("0s")
    } else {
        setTimeLeft(`${(diff/1000).toFixed(0)}s`)
    }
}

window.setInterval(updateTimer, 100)
