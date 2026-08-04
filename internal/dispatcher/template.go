package dispatcher

import (
	"bytes"
	"fmt"
	"text/template"

	giteaingress "github.com/jeeinn/matea/internal/ingress/gitea"
)

// TemplateData is the data available for prompt template rendering.
type TemplateData struct {
	Event   *giteaingress.WebhookEvent
	Issue   *giteaingress.Issue
	PR      *giteaingress.PullRequest
	Comment *giteaingress.Comment
	Repo    *giteaingress.Repository
	Sender  *giteaingress.User
	Task    *TaskData
}

// TaskData contains task-specific information.
type TaskData struct {
	ID       int64
	TaskType string
}

// RenderTemplate renders a Go template string with the given data.
func RenderTemplate(tmplStr string, data *TemplateData) (string, error) {
	if tmplStr == "" {
		return "", nil
	}

	tmpl, err := template.New("prompt").Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}

	return buf.String(), nil
}

// BuildTemplateData creates TemplateData from a webhook event.
func BuildTemplateData(evt *giteaingress.WebhookEvent) *TemplateData {
	data := &TemplateData{
		Event:  evt,
		Repo:   &evt.Repo,
		Sender: &evt.Sender,
	}

	if evt.Issue != nil {
		data.Issue = evt.Issue
	}
	if evt.PR != nil {
		data.PR = evt.PR
	}
	if evt.Comment != nil {
		data.Comment = evt.Comment
	}

	return data
}
