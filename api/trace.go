package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

const traceKey = "trace"

type span struct {
	name     string
	start    time.Time
	duration int64
	childs   []*span
}

func NewTrace(name string) *span {
	return &span{name: name, start: time.Now()}
}

func (s *span) End() {
	s.duration = time.Since(s.start).Microseconds()
}

func (s span) String() string {
	if s.duration == 0 {
		s.End()
	}
	var out []string
	for _, c := range s.childs {
		out = append(out, " ["+c.String()+"]")
	}
	return s.name + "=" + strconv.FormatInt(s.duration, 10) + "us" + strings.Join(out, "")
}

func (s *span) AddSpan(name string) *span {
	c := &span{name: name, start: time.Now()}
	s.childs = append(s.childs, c)
	return c
}

func requestTrace(r *http.Request) *span {
	if t := r.Context().Value(traceKey); t != nil {
		return t.(*span)
	}
	return NewTrace("unknown")
}

func traceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeName := "unknown"
		if route := mux.CurrentRoute(r); route != nil {
			routeName = route.GetName()
		}

		ctx := context.WithValue(r.Context(), traceKey, NewTrace(routeName))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
