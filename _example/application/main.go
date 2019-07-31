package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/go-pkgz/jrpc"
)

type dataRecord struct {
	TS    time.Time
	Value string
}

func main() {

	rpcClient := jrpc.Client{
		API:        "http://127.0.0.1:8080/command",
		Client:     http.Client{},
		AuthUser:   "user",
		AuthPasswd: "password",
	}

	rec := dataRecord{time.Now(), "12345"}
	resp, err := rpcClient.Call("store.save", rec)
	if err != nil {
		panic(err)
	}
	var recID string
	if err = json.Unmarshal(*resp.Result, &recID); err != nil {
		panic(err)
	}
	log.Printf("stored %+v with id=%s", rec, recID)

	if resp, err = rpcClient.Call("store.load", recID); err != nil {
		panic(err)
	}
	if err = json.Unmarshal(*resp.Result, &rec); err != nil {
		panic(err)
	}

	log.Printf("loaded %+v from id=%s", rec, recID)

	if resp, err = rpcClient.Call("store.load", "something"); err != nil {
		log.Printf("can't load for id=something, %s", err)
	}
}
