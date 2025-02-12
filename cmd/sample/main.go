package main

import "net/http"

func main() {
	http.HandleFunc("/v1/route", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://www.json.org/json.json", http.StatusFound)
	})

	http.ListenAndServe(":7070", nil)
}
