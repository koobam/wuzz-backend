package activity

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/koobam/wuzz-backend/internal/pkg/apperr"
	"github.com/koobam/wuzz-backend/internal/pkg/authn"
	"github.com/koobam/wuzz-backend/internal/pkg/httpx"
)

// Record logs every state-changing route listed in rules.go. Mount it on the /backoffice/v1 and /pos/v1 groups before their routes.
//
// It reads the request body (and puts it back) and copies the answer as it goes out, but only for a route in the table; everything
// else passes through untouched. The entry is built after the handler has run, so it knows the real status, the signed-in person and,
// for a create, the id the new thing got. verifier is only used to name who logged out, whose route carries no identity of its own.
func Record(rec *Recorder, verifier *authn.Verifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead || c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		audience, route, ok := splitRoute(c.FullPath())
		r, known := rules[c.Request.Method+" "+route]
		if !ok || !known {
			c.Next()
			return
		}

		var body []byte
		if !r.noBody && c.Request.Body != nil {
			orig := c.Request.Body
			head, _ := io.ReadAll(io.LimitReader(orig, maxBody+1))
			c.Request.Body = struct {
				io.Reader
				io.Closer
			}{io.MultiReader(bytes.NewReader(head), orig), orig}
			if len(head) <= maxBody {
				body = head
			}
		}
		capture := &capturingWriter{ResponseWriter: c.Writer}
		c.Writer = capture

		c.Next()

		e := build(c, r, audience, body, capture.bytes(), verifier)
		// Nobody to hold it to: a mutation refused before anyone was identified is noise (and a way to flood the log). Only a sign-in
		// that failed is worth keeping without a name, since someone guessing codes shows up there.
		if e.Actor.ID == "" && e.Action != ActionSignIn {
			return
		}
		rec.Record(e)
	}
}

// splitRoute turns "/backoffice/v1/services/:id" into ("backoffice", "/services/:id").
func splitRoute(full string) (audience, route string, ok bool) {
	for _, a := range audiences {
		if rest, found := strings.CutPrefix(full, a.prefix); found && strings.HasPrefix(rest, "/") {
			return a.name, rest, true
		}
	}
	return "", "", false
}

// build makes the entry for a finished request.
func build(c *gin.Context, r rule, audience string, reqBody, resBody []byte, verifier *authn.Verifier) Entry {
	status := c.Writer.Status()
	// A handler that fails only records the error; the engine's ErrorHandler writes the response once every middleware, this one
	// included, has returned. So the status that will be sent is the error's, not the 200 the writer still shows.
	if len(c.Errors) > 0 && !c.Writer.Written() {
		status = apperr.From(c.Errors.Last().Err).HTTPStatus()
	}
	outcome := OutcomeOK
	if status >= http.StatusBadRequest {
		outcome = OutcomeFailed
	}

	var answer map[string]any
	if status < http.StatusBadRequest {
		answer = dataObject(resBody)
	}
	var request map[string]any
	if len(reqBody) > 0 {
		_ = json.Unmarshal(reqBody, &request)
	}

	e := Entry{
		CreatedAt: time.Now().UTC(),
		Audience:  audience,
		Actor:     actorOf(c, answer, r, verifier),
		Action:    r.action, EntityType: r.entity,
		EntityID: entityID(c, r, answer, request),
		Outcome:  outcome, Status: status,
		RequestID: c.Writer.Header().Get(httpx.HeaderRequestID),
		IP:        c.ClientIP(),
	}
	e.Summary = Summary{Key: "activity." + r.entity + "." + r.action, Params: summaryParams(r, answer, request)}
	if len(reqBody) > 0 {
		e.Changes = redact(reqBody)
	}
	return e
}

func actorOf(c *gin.Context, answer map[string]any, r rule, verifier *authn.Verifier) Actor {
	if id, ok := authn.FromContext(c.Request.Context()); ok {
		return Actor{ID: id.UserID, Name: id.Name, Roles: nonNil(id.Roles)}
	}
	// A sign-in: nobody is identified yet, but the answer says who just was.
	if r.action == ActionSignIn {
		if u, ok := answer["user"].(map[string]any); ok {
			return Actor{ID: str(u["id"]), Name: str(u["name"]), Roles: roles(u["roles"])}
		}
		return Actor{Roles: []string{}}
	}
	// A sign-out carries only a refresh token; the access token, if still good, names the person.
	if token, found := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer "); found && verifier != nil {
		if id, err := verifier.Parse(strings.TrimSpace(token)); err == nil {
			return Actor{ID: id.UserID, Name: id.Name, Roles: nonNil(id.Roles)}
		}
	}
	return Actor{Roles: []string{}}
}

func entityID(c *gin.Context, r rule, answer, request map[string]any) string {
	if len(r.idParams) > 0 {
		parts := make([]string, len(r.idParams))
		for i, p := range r.idParams {
			parts[i] = c.Param(p)
		}
		return strings.Join(parts, ":")
	}
	if r.idKey != "" {
		if v, ok := answer[r.idKey]; ok {
			return str(v)
		}
		return str(request[r.idKey])
	}
	return ""
}

func summaryParams(r rule, answer, request map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range r.params {
	sources:
		for _, src := range []map[string]any{answer, request} {
			v, ok := src[k]
			if !ok {
				continue
			}
			switch x := v.(type) {
			case string:
				out[k] = cut(x)
			case float64, bool:
				out[k] = x
			}
			break sources
		}
	}
	return out
}

// dataObject is the "data" object of a success envelope, or nil.
func dataObject(body []byte) map[string]any {
	if len(body) == 0 {
		return nil
	}
	var env struct {
		Data map[string]any `json:"data"`
	}
	if json.Unmarshal(body, &env) != nil {
		return nil
	}
	return env.Data
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}

func roles(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, r := range list {
		out = append(out, str(r))
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// capturingWriter copies what the handler writes, up to maxBody, so the middleware can read the answer afterwards.
type capturingWriter struct {
	gin.ResponseWriter
	buf  bytes.Buffer
	over bool
}

func (w *capturingWriter) Write(b []byte) (int, error) {
	w.keep(b)
	return w.ResponseWriter.Write(b)
}

func (w *capturingWriter) WriteString(s string) (int, error) {
	w.keep([]byte(s))
	return w.ResponseWriter.WriteString(s)
}

func (w *capturingWriter) keep(b []byte) {
	if w.over {
		return
	}
	if w.buf.Len()+len(b) > maxBody {
		w.over = true
		w.buf.Reset()
		return
	}
	w.buf.Write(b)
}

func (w *capturingWriter) bytes() []byte {
	if w.over {
		return nil
	}
	return w.buf.Bytes()
}
