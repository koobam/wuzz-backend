// Package docs serves the gateway's OpenAPI spec and a Swagger UI over it. The
// spec is written by hand (openapi.yaml, next to this file), so an endpoint added
// to the router needs a matching entry there.
package docs

import (
	_ "embed"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/goccy/go-yaml"
)

//go:embed openapi.yaml
var spec []byte

const page = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Wuzz API</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui.css">
</head>
<body>
  <div id="ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui-bundle.js"></script>
  <script>
    SwaggerUIBundle({ url: '/openapi.yaml', dom_id: '#ui', persistAuthorization: true });
  </script>
</body>
</html>`

// Register mounts /docs and /openapi.yaml. Callers decide whether to: the spec
// describes staff routes, so only staging turns it on.
func Register(engine *gin.Engine) {
	engine.GET("/docs", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(page))
	})
	engine.GET("/openapi.yaml", func(c *gin.Context) {
		c.Data(http.StatusOK, "application/yaml; charset=utf-8", spec)
	})
}

// Operations lists "METHOD /path" for every operation the spec documents (path parameters in
// braces, as OpenAPI writes them). A test compares it with the router, so a route added
// without a spec entry fails the build.
func Operations() (map[string]bool, error) {
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(spec, &doc); err != nil {
		return nil, fmt.Errorf("openapi.yaml: %w", err)
	}
	ops := map[string]bool{}
	for path, item := range doc.Paths {
		for method := range item {
			switch method {
			case "get", "post", "put", "patch", "delete":
				ops[strings.ToUpper(method)+" "+path] = true
			}
		}
	}
	return ops, nil
}
