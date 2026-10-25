//go:build !goexperiment.jsonv2

package gateway

// encoding/json with jsonv2 escapes raw JS separators independently of HTML.
const rawJSONEscapesJS = false
