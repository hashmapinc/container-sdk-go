package container_sdk_go

import (
	"errors"
	"fmt"
	ws "github.com/gorilla/websocket"
	"time"
)

type ioFogWsClient struct {
	url_base_ws         string
	url_get_control_ws  string
	url_get_message_ws  string
	wsControl           *ws.Conn
	wsMessage           *ws.Conn
	wsControlAttempt    uint
	wsMessageAttempt    uint
	writeMessageChannel chan []byte
}

func newIoFogWsClient(id string, ssl bool, host string, port int) *ioFogWsClient {
	client := ioFogWsClient{}
	protocol_ws := WS
	if ssl {
		protocol_ws = WSS
	}
	client.url_base_ws = fmt.Sprintf("%s://%s:%d", protocol_ws, host, port)
	client.url_get_control_ws = fmt.Sprint(client.url_base_ws, URL_GET_CONTROL_WS, id)
	client.url_get_message_ws = fmt.Sprint(client.url_base_ws, URL_GET_MESSAGE_WS, id)
	return &client
}

func (client *ioFogWsClient) sendMessage(msg *IoMessage) (e error) {
	if client.wsMessage == nil {
		return errors.New("Socket is not initialized")
	}
	bytesToSend, err := PrepareMessageForSendingViaSocket(msg)
	if err != nil {
		return err
	}
	defer func() {
		if r := recover(); r != nil {
			logger.Println(r)
			e = errors.New("Error while sending message")
		}
	}()
	client.writeMessageChannel <- bytesToSend
	return nil
}

func (client *ioFogWsClient) connectToControlWs(signalChannel chan<- byte) {
	for {
		if client.wsControl != nil {
			client.wsControl.Close()
		}
		conn, _, err := ws.DefaultDialer.Dial(client.url_get_control_ws, nil)
		if conn == nil {
			logger.Println(err.Error(), "Reconnecting to control ws...")
			sleepTime := 1 << client.wsControlAttempt * WS_CONNECT_TIMEOUT
			if client.wsControlAttempt < WS_ATTEMPT_LIMIT {
				client.wsControlAttempt++
			}
			time.Sleep(sleepTime)
		} else {
			client.wsControlAttempt = 0
			client.wsControl = conn
			setCustomPingHandler(client.wsControl)
			errChanel := make(chan byte, 2)
			writeChannel := make(chan []byte)
			go client.listenControlSocket(errChanel, signalChannel, writeChannel)
			go client.writeControlSocket(errChanel, writeChannel)
		loop:
			for {
				select {
				case <-errChanel:
					logger.Println("Reconnecting after control ws corruption")
					client.wsControl.Close()
					break loop
				}
			}
		}
	}
}

func (client *ioFogWsClient) connectToMessageWs(messageChannel chan<- *IoMessage, receiptChannel chan<- *PostMessageResponse) {
	for {
		if client.wsMessage != nil {
			client.wsControl.Close()
		}
		conn, _, err := ws.DefaultDialer.Dial(client.url_get_message_ws, nil)
		if conn == nil {
			logger.Println(err.Error(), "Reconnecting to message ws...")
			sleepTime := 1 << client.wsMessageAttempt * WS_CONNECT_TIMEOUT
			if client.wsMessageAttempt < WS_ATTEMPT_LIMIT {
				client.wsMessageAttempt++
			}
			time.Sleep(sleepTime)
		} else {
			client.wsMessageAttempt = 0
			client.wsMessage = conn
			setCustomPingHandler(client.wsMessage)
			errChannel := make(chan byte, 2)
			writeChannel := make(chan []byte, 20)
			client.writeMessageChannel = writeChannel
			go client.listenMessageSocket(errChannel, messageChannel, receiptChannel, writeChannel)
			go client.writeMessageSocket(errChannel, writeChannel)
		loop:
			for {
				select {
				case <-errChannel:
					logger.Println("Reconnecting after message ws corruption")
					client.wsMessage.Close()
					break loop
				}
			}
		}
	}
}

func (client *ioFogWsClient) listenControlSocket(errChanel chan<- byte, signalChannel chan<- byte, writeChannel chan<- []byte) {
	for {
		_, p, err := client.wsControl.ReadMessage()
		if err != nil {
			logger.Println("Control ws read error:", err.Error())
			errChanel <- 0
			close(writeChannel)
			return
		}
		if p[0] == CODE_CONTROL_SIGNAL {
			signalChannel <- p[0]
			writeChannel <- []byte{CODE_ACK}
		}
	}
}

func (client *ioFogWsClient) writeControlSocket(errChanel chan<- byte, writeChannel <-chan []byte) {
	for data := range writeChannel {
		err := client.wsControl.WriteMessage(ws.BinaryMessage, data)
		if err != nil {
			logger.Println("Control ws write error:", err.Error())
			errChanel <- 0
			return
		}
	}
}

func (client *ioFogWsClient) listenMessageSocket(errChanel chan<- byte, messageChannel chan<- *IoMessage, receiptChannel chan<- *PostMessageResponse, writeChannel chan<- []byte) {
	for {
		_, p, err := client.wsMessage.ReadMessage()
		if err != nil {
			logger.Println("Message ws read error:", err.Error())
			errChanel <- 0
			close(writeChannel)
			return
		}
		if p[0] == CODE_MSG {
			msg, err := GetMessageReceivedViaSocket(p)
			if err != nil {
				logger.Println(err.Error())
			}
			messageChannel <- msg
			writeChannel <- []byte{CODE_ACK}
		} else if p[0] == CODE_RECEIPT {
			receiptResponse, err := getReceiptReceivedViaSocket(p)
			if err != nil {
				logger.Println(err.Error())
			}
			receiptChannel <- receiptResponse
			writeChannel <- []byte{CODE_ACK}
		}
	}
}

func (client *ioFogWsClient) writeMessageSocket(errChanel chan<- byte, writeChannel <-chan []byte) {
	for data := range writeChannel {
		err := client.wsMessage.WriteMessage(ws.BinaryMessage, data)
		if err != nil {
			logger.Println("Message ws write error:", err.Error())
			errChanel <- 0
			return
		}
	}
}
