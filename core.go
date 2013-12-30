// Package mwclient provides methods for interacting with the MediaWiki API.
package mwclient

import (
	"errors"
	"fmt"
	simplejson "github.com/bitly/go-simplejson"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
)

// If you modify this package, please change the user agent.
const DefaultUserAgent = "go-mwclient (https://github.com/cgtdk/go-mwclient) by meta:User:Cgtdk"

type Wiki struct {
	client            *http.Client
	cjar              *cookiejar.Jar
	ApiUrl            *url.URL
	format, UserAgent string
	Tokens            map[string]string
}

// NewWiki returns an initialized Wiki object. If the provided API url is an
// invalid URL (as defined by the net/url package), then it will panic
// with the error from url.Parse().
func NewWiki(inUrl string) *Wiki {
	cjar, _ := cookiejar.New(nil)
	apiurl, err := url.Parse(inUrl)
	if err != nil {
		panic(err) // Yes, this is bad, but so is using bad URLs and I don't want two return values.
	}
	return &Wiki{
		&http.Client{nil, nil, cjar},
		cjar,
		apiurl,
		"json",
		DefaultUserAgent,
		map[string]string{},
	}
}

// call makes a GET or POST request to the Mediawiki API (depending on whether
// the post argument is true or false (if true, it will POST).
func (w *Wiki) call(params url.Values, post bool) (*simplejson.Json, error) {
	params.Set("format", w.format)

	// Make a POST or GET request depending on the "post" parameter.
	var httpMethod string
	if post {
		httpMethod = "POST"
	} else {
		httpMethod = "GET"
	}

	var req *http.Request
	var err error
	if post {
		req, err = http.NewRequest(httpMethod, w.ApiUrl.String(), strings.NewReader(params.Encode()))
	} else {
		req, err = http.NewRequest(httpMethod, fmt.Sprintf("%s?%s", w.ApiUrl.String(), params.Encode()), nil)
	}
	if err != nil {
		log.Printf("Unable to make request: %s\n", err)
		return nil, err
	}

	// Set headers on request
	req.Header.Set("User-Agent", w.UserAgent)
	if post {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	// Set any old cookies on the request
	for _, cookie := range w.cjar.Cookies(w.ApiUrl) {
		req.AddCookie(cookie)
	}

	// Make the request
	resp, err := w.client.Do(req)
	defer resp.Body.Close()
	if err != nil {
		log.Printf("Error during %s: %s\n", httpMethod, err)
		return nil, err
	}

	// Set any new cookies
	w.cjar.SetCookies(req.URL, resp.Cookies())

	jsonResp, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading from resp.Body: %s\n", err)
		return nil, err
	}

	js, err := simplejson.NewJson(jsonResp)
	if err != nil {
		log.Printf("Error during JSON parsing: %s\n", err)
		return nil, err
	}

	return js, nil
}

// Get wraps the w.call method to make it do a GET request.
func (w *Wiki) Get(params url.Values) (*simplejson.Json, error) {
	return w.call(params, false)
}

// GetCheck wraps the w.call method to make it do a GET request
// and checks for API errors/warnings using the ErrorCheck function.
// The returned boolean will be true if no API errors or warnings are found.
func (w *Wiki) GetCheck(params url.Values) (*simplejson.Json, error, bool) {
	return ErrorCheck(w.call(params, false))
}

// Post wraps the w.call method to make it do a POST request.
func (w *Wiki) Post(params url.Values) (*simplejson.Json, error) {
	return w.call(params, true)
}

// PostCheck wraps the w.call method to make it do a POST request
// and checks for API errors/warnings using the ErrorCheck function.
// The returned boolean will be true if no API errors or warnings are found.
func (w *Wiki) PostCheck(params url.Values) (*simplejson.Json, error, bool) {
	return ErrorCheck(w.call(params, true))
}

// ErrorCheck checks for API errors and warnings, and returns false as its third
// return value if any are found. Otherwise it returns true.
// ErrorCheck does not modify the json and err parameters, but merely passes them through,
// so it can be used to wrap the Post and Get methods.
func ErrorCheck(json *simplejson.Json, err error) (*simplejson.Json, error, bool) {
	apiok := true

	if _, ok := json.CheckGet("error"); ok {
		apiok = false
	}

	if _, ok := json.CheckGet("warnings"); ok {
		apiok = false
	}

	return json, err, apiok
}

// Login attempts to login using the provided username and password.
func (w *Wiki) Login(username, password string) error {

	// By using a closure, we avoid requiring the public Login method to have a token parameter.
	var loginFunc func(token string) error

	loginFunc = func(token string) error {
		v := url.Values{
			"action":     {"login"},
			"lgname":     {username},
			"lgpassword": {password},
		}
		if token != "" {
			v.Set("lgtoken", token)
		}

		resp, err := w.Post(v)
		if err != nil {
			return err
		}

		if lgResult, _ := resp.Get("login").Get("result").String(); lgResult != "Success" {
			if lgResult == "NeedToken" {
				lgToken, _ := resp.Get("login").Get("token").String()
				return loginFunc(lgToken)
			} else {
				return errors.New(lgResult)
			}
		}

		return nil
	}

	return loginFunc("")
}

// Logout logs out. It does not take into account whether or not a user is actually
// logged in (because it is irrelevant). Always returns true.
func (w *Wiki) Logout() bool {
	w.Get(url.Values{"action": {"logout"}})
	return true
}

// GetToken returns a specified token (and an error if this is not possible).
// If the token is not already available in the Wiki.Tokens map,
// it will attempt to retrieve it via the API.
// tokenName should be "edit" (or whatever), not "edittoken".
func (w *Wiki) GetToken(tokenName string) (string, error) {
	if _, ok := w.Tokens[tokenName]; ok {
		log.Println("Got from map")
		return w.Tokens[tokenName], nil
	}

	parameters := url.Values{
		"action": {"tokens"},
		"type":   {tokenName},
	}

	resp, err, apiok := ErrorCheck(w.Get(parameters))
	if err != nil {
		return "", err
	}
	if !apiok {
		// Check for errors
		if err, ok := resp.CheckGet("error"); ok {
			newError := fmt.Errorf("%s: %s", err.Get("code").MustString(), err.Get("info").MustString())
			return "", newError
		}

		// Check for warnings
		if warnings, ok := resp.CheckGet("warnings"); ok {
			newError := fmt.Errorf(warnings.GetPath("tokens", "*").MustString())
			return "", newError
		}
	}

	token, err := resp.GetPath("tokens", tokenName+"token").String()
	if err != nil {
		// This really shouldn't happen.
		return "", fmt.Errorf("Error occured while converting token to string: %s", err)
	}
	w.Tokens[tokenName] = token
	return token, nil
}
