const WS_URL = wsUrl("/api/ws")
let socket = connectSocket(WS_URL)
const MAX_RETRIES = 5
const INITIAL_TIMEOUT = 1000
let retryTimeoutMs = INITIAL_TIMEOUT
let retries = 0

let currentTeam = ""
let token = ""
let team = ""

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
        console.log(`Got msg: ${msg}`)
        const lines = msg.split("\n")
        switch (lines[0]) {
            case "GameState":
                const cells = lines[1].slice(1,-1).split(" ")
                for (let row = 0; row < 3; row++) {
                    for (let col = 0; col < 3; col++) {
                        const i  = row * 3 + col
                        let cell = getCell(row, col)
                        if (cells[i] == "1") {
                            cell.innerHTML = "O"
                        } else if (cells[i] == "2") {
                            cell.innerHTML = "X"
                        } else {
                            cell.innerHTML = ""
                        }
                    }
                }
                break
            case "Board":
                const values = lines[1].split(",")
                for (let row = 0; row < 3; row++) {
                    for (let col = 0; col < 3; col++) {
                        i = row * 3 + col
                        getCell(row, col).innerHTML = values[i]
                    }
                }
                break
            case "Round":
                token = lines[1]
                // TODO: parse date
                currentTeam = lines[3]
                break
            case "Move":
                let [row, col] = lines[1].split(",")
                getCell(row, col).innerHTML = lines[2]
                break
            case "VoteCounts":
                console.log(`VoteCounts: ${lines[2]}`)
                break
            case "Team":
                team = lines[1]
                break
            default:
                setMessageBox(msg)
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
    let cell = getCell(row, col)
    if (currentTeam != team || cell.innerHTML != "") {
        return
    }
    const msg = `Vote\n${token}\n${row},${col}`
    console.log(`Sending message: ${msg}`)
    socket.send(msg)
}

function getCell(row, col) {
    return document.getElementById(`tttCell-${row}-${col}`)
}

function setMessageBox(msg) {
    document.getElementById(`message-box`).textContent = msg
}

for (let row = 0; row < 3; row++) {
    for (let col = 0; col < 3; col++) {
        const cell = getCell(row, col)
        cell.innerHTML = ""
        cell.onclick = () => cellOnClick(row, col)
    }
}
