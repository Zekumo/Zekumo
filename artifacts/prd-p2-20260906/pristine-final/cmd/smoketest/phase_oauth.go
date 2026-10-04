package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

func phaseOAuth(s *state) {
	// OAuth2: client registration, PKCE authorization-code flow, refresh
	// rotation with reuse detection, and account-side revocation.
	var oc struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	step("admin create oauth client", call("POST", "/admin/api/oauth/clients", s.adminToken,
		map[string]any{"name": "第三方启动器", "redirect_urls": "https://thirdparty.example.com/cb", "confidential": true}, &oc))
	// OAuth clients are platform-wide, so deleting the game at the end does
	// not reach them; without this every run would leave one behind.
	s.oauthClientID = oc.ClientID

	verifier := "test-verifier-" + s.suffix + "-0123456789abcdef0123456789abcdef"
	chSum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(chSum[:])
	var approved struct {
		Code string `json:"code"`
	}
	step("oauth approve (consent)", call("POST", "/oauth/api/approve", s.accountTok,
		map[string]string{"client_id": oc.ClientID, "redirect_uri": "https://thirdparty.example.com/cb",
			"code_challenge": challenge, "code_challenge_method": "S256"}, &approved))
	step("approve rejects bad redirect", expectErr(call("POST", "/oauth/api/approve", s.accountTok,
		map[string]string{"client_id": oc.ClientID, "redirect_uri": "https://evil.example.com/cb"}, nil)))

	tokenRes, err := http.PostForm(base+"/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {approved.Code},
		"redirect_uri": {"https://thirdparty.example.com/cb"},
		"client_id":    {oc.ClientID}, "client_secret": {oc.ClientSecret},
		"code_verifier": {verifier},
	})
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		Error        string `json:"error"`
	}
	if err == nil {
		_ = json.NewDecoder(tokenRes.Body).Decode(&tok)
		tokenRes.Body.Close()
		if tok.Error != "" || tok.AccessToken == "" {
			err = fmt.Errorf("token exchange failed: %+v", tok)
		}
	}
	step("oauth code exchange (form + PKCE)", err)

	var ui struct {
		Username string `json:"username"`
	}
	step("oauth userinfo", call("GET", "/oauth/userinfo", tok.AccessToken, nil, &ui))
	step("userinfo matches account", boolErr(ui.Username == "carol_"+s.suffix, "got username %q", ui.Username))

	var tok2 struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	step("oauth refresh rotates", call("POST", "/oauth/token", "",
		map[string]string{"grant_type": "refresh_token", "refresh_token": tok.RefreshToken,
			"client_id": oc.ClientID, "client_secret": oc.ClientSecret}, &tok2))
	step("old refresh token reuse rejected", expectErr(call("POST", "/oauth/token", "",
		map[string]string{"grant_type": "refresh_token", "refresh_token": tok.RefreshToken,
			"client_id": oc.ClientID, "client_secret": oc.ClientSecret}, nil)))
	step("reuse revoked whole family", expectErr(call("POST", "/oauth/token", "",
		map[string]string{"grant_type": "refresh_token", "refresh_token": tok2.RefreshToken,
			"client_id": oc.ClientID, "client_secret": oc.ClientSecret}, nil)))

	var auths struct {
		Authorizations []struct {
			ClientID string `json:"client_id"`
		} `json:"authorizations"`
	}
	step("account lists authorizations", call("GET", "/sso/api/authorizations", s.accountTok, nil, &auths))
	step("authorization recorded", boolErr(len(auths.Authorizations) == 1 && auths.Authorizations[0].ClientID == oc.ClientID,
		"unexpected authorizations: %+v", auths.Authorizations))
	step("account revokes authorization", call("DELETE", "/sso/api/authorizations/"+oc.ClientID, s.accountTok, nil, nil))
	step("revoke kills live access token", expectErr(call("GET", "/oauth/userinfo", tok.AccessToken, nil, nil)))
}
