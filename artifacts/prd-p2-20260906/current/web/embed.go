// Package web embeds the admin console static files into the server binary.
package web

import "embed"

//go:embed admin sso oauth
var FS embed.FS
