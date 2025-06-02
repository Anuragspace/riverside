package video

import (
	"log"
	"sync"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
)

var AllRooms RoomMap

type response struct {
	RoomID string `json:"roomID"`
}

type BroadcastMessage struct {
	Message map[string]interface{}
	RoomID  string
	Client  *websocket.Conn
}

type Client struct {
	Conn  *websocket.Conn
	Mutex sync.Mutex
}

var (
	broadcast     = make(chan BroadcastMessage)
	broadcastOnce sync.Once
)

func broadcaster() {
	for msg := range broadcast {
		clients := AllRooms.Get(msg.RoomID)
		for i := 0; i < len(clients); i++ {
			client := &clients[i]
			// Don't send message back to sender
			if client.Conn == msg.Client {
				continue
			}

			client.Mutex.Lock()
			err := client.Conn.WriteJSON(msg.Message)
			client.Mutex.Unlock()

			if err != nil {
				log.Printf("Broadcast error: %v. Closing connection.", err)
				client.Conn.Close()
				// Remove client from room
				AllRooms.RemoveClient(msg.RoomID, client.Conn)
			}
		}
	}
}

func CreateRoomRequestHandler(c *fiber.Ctx) error {
	c.Set("Access-Control-Allow-Origin", "*")

	roomID := AllRooms.CreateRoom()
	return c.JSON(response{RoomID: roomID})
}

func JoinRoomRequestHandler(c *fiber.Ctx) error {
	roomID := c.Query("roomID")
	if roomID == "" {
		log.Println("roomID is missing, unable to join the call")
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "roomID is required",
		})
	}

	if websocket.IsWebSocketUpgrade(c) {
		c.Locals("roomID", roomID)
		return c.Next()
	}
	return fiber.ErrUpgradeRequired
}

func WebSocketJoinHandler(c *websocket.Conn) {
	roomID := c.Query("roomID")
	if roomID == "" {
		log.Println("roomID is missing in WebSocket connection")
		c.Close()
		return
	}

	log.Printf("New WebSocket connection for room: %s", roomID)

	participants := AllRooms.Get(roomID)
	if participants == nil {
		log.Printf("Room %s does not exist", roomID)
		c.Close()
		return
	}

	// Notify existing participants of a new join
	for i := 0; i < len(participants); i++ {
		participant := &participants[i]
		participant.Mutex.Lock()
		err := participant.Conn.WriteJSON(map[string]interface{}{
			"join": true,
		})
		participant.Mutex.Unlock()
		if err != nil {
			log.Printf("Error sending join message: %v", err)
		}
	}

	// Add new participant to the room
	AllRooms.InsertInRoom(roomID, false, c)

	// Start broadcaster once
	broadcastOnce.Do(func() {
		go broadcaster()
	})

	for {
		var msg BroadcastMessage
		err := c.ReadJSON(&msg.Message)
		if err != nil {
			log.Printf("Read error in room %s: %v", roomID, err)
			break
		}

		msg.Client = c
		msg.RoomID = roomID

		broadcast <- msg
	}

	// Cleanup after connection closes
	AllRooms.RemoveClient(roomID, c)
	c.Close()
}
