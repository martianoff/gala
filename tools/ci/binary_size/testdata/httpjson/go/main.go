package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"unicode/utf8"
)

// Greeting is the JSON body /greet answers with.
type Greeting struct {
	Message string `json:"message"`
	Count   int    `json:"count"`
}

func main() {
	http.HandleFunc("/greet", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		w.Header().Set("Content-Type", "application/json")
		body, err := json.Marshal(Greeting{Message: "Hello, " + name + "!", Count: utf8.RuneCountInString(name)})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write(body)
	})
	if err := http.ListenAndServe(":8080", nil); err != nil {
		fmt.Println("error:", err)
	}
}
