//go:build weknora_structured_output

package service

import (
	extension "github.com/Tencent/WeKnora/internal/extensions/structuredoutput"
	port "github.com/Tencent/WeKnora/internal/structuredoutput"
)

// The standard build has no import of the extension implementation and keeps
// the port's passthrough default. A tagged build registers the optional
// implementation; its runtime mode still defaults to off.
func init() {
	port.Register(extension.NewFromEnvironment())
}
