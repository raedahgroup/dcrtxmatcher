package main

import (
	"fmt"
	"net/http"

	"github.com/golang/protobuf/proto"
	"github.com/gorilla/websocket"
	pb "github.com/raedahgroup/dcrtxmatcher/api/messages"

	"github.com/raedahgroup/dcrtxmatcher/coinjoin"
)

// This code is used for testing purpose of initial of project to
// communicate between client and server.
func main() {

	dialer := websocket.Dialer{}
	ws, _, err := dialer.Dial("ws://127.0.0.1:8475/ws", http.Header{})
	if err != nil {
		fmt.Println("Error connecting:", err)
		return
	}

	fmt.Println("Connected!")
	defer ws.Close()

	for {

		_, msg, err := ws.ReadMessage()
		if err != nil {
			fmt.Printf("Can not ReadMessage: %v\n", err)
			break
		}

		message, err := coinjoin.ParseMessage(msg)

		if err != nil {
			fmt.Printf("Can not ParseMessage: %v\n", err)
			break
		}

		switch message.MsgType {
		case coinjoin.S_JOIN_RESPONSE:
			joinRes := &pb.CoinJoinRes{}
			err := proto.Unmarshal(message.Data, joinRes)
			if err != nil {
				break
			}

			// Send key exchange cmd
			keyex := &pb.KeyExchangeReq{
				SessionId: joinRes.SessionId,
				PeerId:    joinRes.PeerId,
				Pk:        []byte("pkkey"),
			}

			data, err := proto.Marshal(keyex)
			if err != nil {
				fmt.Printf("error Unmarshal joinRes: %v", err)
				break
			}

			message := coinjoin.NewMessage(coinjoin.C_KEY_EXCHANGE, data)

			if err := ws.WriteMessage(websocket.BinaryMessage, message.ToBytes()); err != nil {
				fmt.Printf("error WriteMessage: %v", err)
				break
			}

		case coinjoin.S_KEY_EXCHANGE:
			keyex := &pb.KeyExchangeRes{}
			err := proto.Unmarshal(message.Data, keyex)
			if err != nil {
				fmt.Printf("error Unmarshal joinRes: %v", err)
			}

		case coinjoin.S_HASHES_VECTOR:
		case coinjoin.S_MIXED_TX:
		case coinjoin.S_JOINED_TX:

		}
	}

}
