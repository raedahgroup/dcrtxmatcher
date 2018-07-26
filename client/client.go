package main

import (
	"fmt"
	"net/http"
	//"time"

	"github.com/golang/protobuf/proto"
	"github.com/gorilla/websocket"
	pb "github.com/raedahgroup/dcrtxmatcher/api/messages"

	"github.com/raedahgroup/dcrtxmatcher/coinjoin"
)

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
			fmt.Println("ReadMessage error", err)
			break
		}

		message, err := coinjoin.ParseMessage(msg)

		if err != nil {
			fmt.Printf("error ParseMessage: %v", err)
		}

		switch message.MsgType {
		case coinjoin.S_JOIN_RESPONSE:
			fmt.Println("S_JOIN_RESPONSE")
			joinRes := &pb.CoinJoinRes{}
			err := proto.Unmarshal(message.Data, joinRes)
			if err != nil {
				fmt.Printf("error Unmarshal joinRes: %v", err)
				break
			}

			fmt.Println("CoinJoinRes", joinRes)

			//send key exchange cmd
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

			//time.Sleep(time.Second * time.Duration(5))
			if err := ws.WriteMessage(websocket.BinaryMessage, message.ToBytes()); err != nil {
				fmt.Printf("error WriteMessage: %v", err)
				break
			}

		case coinjoin.S_KEY_EXCHANGE:
			fmt.Println("S_KEY_EXCHANGE")
			keyex := &pb.KeyExchangeRes{}
			err := proto.Unmarshal(message.Data, keyex)
			if err != nil {
				fmt.Printf("error Unmarshal joinRes: %v", err)
			}

			fmt.Println("KeyExchangeRes", keyex)

		case coinjoin.S_HASHES_VECTOR:
		case coinjoin.S_MIXED_TX:
		case coinjoin.S_JOINED_TX:

		}
	}

}
