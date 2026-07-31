package socialconnections

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const linkedInFixtureMemberID = "urn:li:person:member42"

func newLinkedInRefreshFailureAdapter(
	t *testing.T,
	status int,
	body string,
) *LinkedInAdapter {
	t.Helper()
	adapter, err := NewLinkedInAdapter(LinkedInAdapterConfig{
		ClientID:                    "fixture-client",
		ClientSecret:                "fixture-secret",
		RedirectURL:                 "https://api.example.test/social/callback",
		APIVersion:                  "202607",
		ProgrammaticRefreshApproved: true,
		AuthorizationURL:            "https://linkedin.fixture/oauth/v2/authorization",
		TokenURL:                    "https://linkedin.fixture/oauth/v2/accessToken",
		UserInfoURL:                 "https://linkedin.fixture/v2/userinfo",
		APIBaseURL:                  "https://linkedin.fixture",
		HTTPClient: &http.Client{Transport: professionalRoundTripFunc(
			func(request *http.Request) (*http.Response, error) {
				switch request.URL.Path {
				case "/oauth/v2/accessToken":
					raw, err := io.ReadAll(request.Body)
					if err != nil {
						t.Fatal(err)
					}
					values, err := url.ParseQuery(string(raw))
					if err != nil {
						t.Fatal(err)
					}
					if values.Get("client_secret") != "fixture-secret" {
						t.Fatal("LinkedIn token exchange omitted server-side secret")
					}
					if values.Get("grant_type") == "refresh_token" {
						return fixtureJSONResponse(status, body), nil
					}
					return fixtureJSONResponse(http.StatusOK, `{
						"access_token":"fixture-access",
						"expires_in":3600,
						"refresh_token":"fixture-refresh",
						"scope":"`+strings.Join(linkedInAtomicScopes, " ")+`"
					}`), nil
				case "/v2/userinfo":
					return fixtureJSONResponse(http.StatusOK, `{
						"sub":"member42",
						"name":"Ada Lovelace"
					}`), nil
				case "/rest/organizationAcls":
					return fixtureJSONResponse(http.StatusOK, `{
						"elements":[],
						"paging":{"start":0,"count":0,"total":0}
					}`), nil
				default:
					t.Fatalf("unexpected LinkedIn request %s", request.URL.String())
					return nil, nil
				}
			},
		)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}
