package server

import (
	"embed"
	"io/fs"
	"net/http"
	"github.com/gin-gonic/gin"
)

//go:embed all:web
var webFS embed.FS

func (s *Server) mountStatic() {
	sub, err := fs.Sub(webFS, "web")
	if err != nil { panic(err) }
	fileServer := http.FileServer(http.FS(sub))
	s.Engine.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if path == "/" { path = "/index.html" }
		if _, err := fs.Stat(sub, path[1:]); err == nil {
			fileServer.ServeHTTP(c.Writer, c.Request); return
		}
		c.Request.URL.Path = "/"
		fileServer.ServeHTTP(c.Writer, c.Request)
	})
}