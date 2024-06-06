const WS_URL = wsUrl("/api/ws")
let socket = connectSocket(WS_URL)
const MAX_RETRIES = 5
const INITIAL_TIMEOUT = 1000
let retryTimeoutMs = INITIAL_TIMEOUT
let retries = 0

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
        if (lines[0] == "GameState") {
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
        } else {
            const arr = event.data.split(",")
            getCell(arr[1], arr[2]).innerHTML = arr[0]
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
    const cell = getCell(row, col)
    if (cell.innerHTML != "X" && cell.innerHTML != "O") {
        cell.innerHTML = "O"
        const msg = `${row},${col}`
        console.log(`Sending message: ${msg}`)
        socket.send(msg)
    }
}

function getCell(row, col) {
    return document.getElementById(`tttCell-${row}-${col}`)
}

for (let row = 0; row < 3; row++) {
    for (let col = 0; col < 3; col++) {
        const cell = getCell(row, col)
        cell.onclick = () => cellOnClick(row, col)
    }
}
