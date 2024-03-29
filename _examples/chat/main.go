package main

import (
	"net/http"

	"github.com/colin1989/battery"
	"github.com/colin1989/battery/blog"
	"github.com/colin1989/battery/constant"
	"github.com/colin1989/battery/facade"
)

func main() {
	app := battery.NewApp(battery.WithGate([]facade.Acceptors{
		{"0.0.0.0:2250", [2]string{}, constant.AcceptorTypeWS},
	}))
	app.Register(&Room{})

	http.Handle("/web/", http.StripPrefix("/web/", http.FileServer(http.Dir("web"))))
	go http.ListenAndServe(":2251", nil)
	blog.Infof("http run. http://%s", "localhost:2251/web/")

	app.Start()
	defer app.Shutdown()
}
