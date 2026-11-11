package middleware

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

// rewriteTaskPluginJSONModel patches the top-level JSON "model" field and
// replaces BodyStorage. Non-JSON bodies are left untouched.
//
// The patch itself lives in common.RewriteBodyJSONModel so the relay
// pass-through branches (which forward raw client bytes and would otherwise
// skip model_mapping addressing) share one implementation instead of keeping
// a private copy of the same sjson + BodyStorage swap.
func rewriteTaskPluginJSONModel(c *gin.Context, spelling string) error {
	_, err := common.RewriteBodyJSONModel(c, spelling)
	return err
}
