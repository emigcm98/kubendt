package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Our own Swagger UI page. gin-swagger can't enable the operations filter or
// hide the "Explore" topbar (a spec loader, not a search), so we use BaseLayout
// and turn on filter ourselves.
const swaggerIndexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>KubeNDT API</title>
  <link rel="stylesheet" type="text/css" href="./swagger-ui.css">
  <link rel="icon" type="image/png" href="./favicon-32x32.png" sizes="32x32">
  <link rel="icon" type="image/png" href="./favicon-16x16.png" sizes="16x16">
</head>
<body>
<div id="swagger-ui"></div>
<script src="./swagger-ui-bundle.js"></script>
<script>
  // The default filter only matches tags. This makes it also match the path,
  // summary and operationId, so "deploy" finds POST /deploy-network.
  var OperationFilterPlugin = function () {
    return {
      fn: {
        opsFilter: function (taggedOps, phrase) {
          var p = (phrase || "").toLowerCase();
          return taggedOps
            .map(function (tagObj, tag) {
              if (tag.toLowerCase().indexOf(p) !== -1) return tagObj;
              var ops = tagObj.get("operations").filter(function (op) {
                var path = (op.get("path") || "").toLowerCase();
                var summary = (op.getIn(["operation", "summary"]) || "").toLowerCase();
                var opId = (op.getIn(["operation", "operationId"]) || "").toLowerCase();
                return path.indexOf(p) !== -1 || summary.indexOf(p) !== -1 || opId.indexOf(p) !== -1;
              });
              return tagObj.set("operations", ops);
            })
            .filter(function (tagObj) {
              return tagObj.get("operations").size > 0;
            });
        }
      }
    };
  };

  window.onload = function () {
    window.ui = SwaggerUIBundle({
      url: "doc.json",
      dom_id: "#swagger-ui",
      deepLinking: true,
      filter: true,
      persistAuthorization: true,
      docExpansion: "list",
      defaultModelsExpandDepth: 1,
      presets: [SwaggerUIBundle.presets.apis],
      plugins: [OperationFilterPlugin],
      layout: "BaseLayout"
    });

    // swagger-ui hardcodes the filter placeholder to "Filter by tag", but ours
    // matches path and summary too. Relabel it when the input (re)appears.
    var relabel = function () {
      var input = document.querySelector(".operation-filter-input");
      if (input && input.placeholder !== "Search endpoints") {
        input.placeholder = "Search endpoints";
      }
    };
    new MutationObserver(relabel).observe(document.getElementById("swagger-ui"), {
      childList: true,
      subtree: true
    });
  };
</script>
</body>
</html>`

// SwaggerUI serves our index at the /swagger root and index.html, and delegates
// doc.json and the UI assets to gin-swagger.
func SwaggerUI(delegate gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Param("any") {
		case "", "/", "/index.html":
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(http.StatusOK, swaggerIndexHTML)
		default:
			delegate(c)
		}
	}
}
