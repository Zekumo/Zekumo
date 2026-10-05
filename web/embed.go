// Package web embeds the public website and hosted pages into the server binary.
package web

import "embed"

//go:embed admin sso oauth site
var FS embed.FS
