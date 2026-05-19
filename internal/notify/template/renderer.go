package template

import (
	"bytes"
	"log"
	tt "text/template"

	"github.com/Terry-Mao/goim/internal/notify/store"
)

// Renderer renders notification title and content using Go templates.
type Renderer struct {
	store *store.SQLStore
}

// NewRenderer creates a new template renderer backed by the store.
func NewRenderer(st *store.SQLStore) *Renderer {
	return &Renderer{store: st}
}

// RenderedNotification holds the rendered title and content.
type RenderedNotification struct {
	Title   string
	Content string
}

// Render renders a template by name with the given variables.
// Returns the rendered result or fallback title/content on error.
func (r *Renderer) Render(templateName string, vars map[string]interface{}, fallbackTitle, fallbackContent string) RenderedNotification {
	if r.store == nil {
		return RenderedNotification{Title: fallbackTitle, Content: fallbackContent}
	}

	tpl, err := r.store.GetTemplateByName(templateName)
	if err != nil {
		log.Printf("[template] template %q not found, using fallback: %v", templateName, err)
		return RenderedNotification{Title: fallbackTitle, Content: fallbackContent}
	}

	title, err := renderTemplate(tpl.TitleTemplate, vars)
	if err != nil {
		log.Printf("[template] failed to render title for %q: %v — using fallback", templateName, err)
		title = fallbackTitle
	}

	content, err := renderTemplate(tpl.ContentTemplate, vars)
	if err != nil {
		log.Printf("[template] failed to render content for %q: %v — using fallback", templateName, err)
		content = fallbackContent
	}

	return RenderedNotification{Title: title, Content: content}
}

func renderTemplate(tmpl string, vars map[string]interface{}) (string, error) {
	t, err := tt.New("t").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, vars); err != nil {
		return "", err
	}
	return buf.String(), nil
}
