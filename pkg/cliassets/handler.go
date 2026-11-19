// Package cliassets serves immutable assets embedded in the gateway build.
package cliassets

import (
	"embed"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/clidistribution"
	"github.com/gin-gonic/gin"
)

//go:embed dist/* install.sh install.ps1
var files embed.FS

func Register(routes *gin.RouterGroup) {
	routes.GET("/cli/manifest", serveManifest)
	routes.GET("/cli/download/:name", serveDownload)
	routes.GET("/cli/install.sh", func(c *gin.Context) { serveScript(c, "install.sh") })
	routes.GET("/cli/install.ps1", func(c *gin.Context) { serveScript(c, "install.ps1") })
}

func manifest() (clidistribution.Manifest, []byte, error) {
	data, err := files.ReadFile("dist/manifest.json")
	var result clidistribution.Manifest
	if err == nil {
		err = common.Unmarshal(data, &result)
	}
	return result, data, err
}

func serveManifest(c *gin.Context) {
	m, data, err := manifest()
	c.Header("Cache-Control", "no-store")
	if err != nil || len(m.Assets) == 0 {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	c.Data(http.StatusOK, "application/json", data)
}

func serveDownload(c *gin.Context) {
	m, _, err := manifest()
	name := c.Param("name")
	a, ok := m.Assets[name]
	if err != nil || !ok || strings.ContainsAny(name, "/\\\r\n\"") {
		c.Status(http.StatusNotFound)
		return
	}
	data, err := files.ReadFile("dist/" + name)
	if err != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("X-Checksum-Sha256", a.SHA256)
	c.Header("Content-Disposition", "attachment; filename=\""+name+"\"")
	c.Data(http.StatusOK, "application/octet-stream", data)
}

func serveScript(c *gin.Context, name string) {
	data, err := files.ReadFile(name)
	if err != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "text/plain; charset=utf-8", data)
}
