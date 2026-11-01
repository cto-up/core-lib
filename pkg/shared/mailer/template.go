package mailer

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"

	utils "github.com/cto-up/core-lib/pkg/shared/util"
	"github.com/gin-gonic/gin"
)

// RenderTemplate executes an HTML template file into a message body.
func RenderTemplate(templateFile string, data any) (string, error) {
	t, err := template.ParseFiles(templateFile)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}
	buf := new(bytes.Buffer)
	if err = t.Execute(buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}
	return buf.String(), nil
}

// RenderDomainTemplate renders the template GetTemplate finds for the request's
// domain.
func RenderDomainTemplate(ctx *gin.Context, templateName string, data any) (string, error) {
	path, err := GetTemplate(ctx, templateName)
	if err != nil {
		return "", fmt.Errorf("failed to find template: %w", err)
	}
	return RenderTemplate(path, data)
}

// GetTemplate finds a template file using hierarchical lookup based on domain and subdomain
// It searches in the following order:
// 1. templates/domain/subdomain/templateName
// 2. templates/domain/templateName
// 3. templates/templateName
//
// For example, if origin is "human.alineo.com" and templateName is "email-verification.html":
// 1. templates/alineo.com/human/email-verification.html
// 2. templates/alineo.com/email-verification.html
// 3. templates/email-verification.html
func GetTemplate(ctx *gin.Context, templateName string) (string, error) {
	domainInfo, err := utils.GetDomainInfo(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get domain info: %w", err)
	}

	searchPaths := make([]string, 0, 3)
	if domainInfo.Domain != "" && domainInfo.Subdomain != "" {
		searchPaths = append(searchPaths, filepath.Join("templates", domainInfo.Domain, domainInfo.Subdomain, templateName))
	}
	if domainInfo.Domain != "" {
		searchPaths = append(searchPaths, filepath.Join("templates", domainInfo.Domain, templateName))
	}
	searchPaths = append(searchPaths, filepath.Join("templates", templateName))

	for _, path := range searchPaths {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("template '%s' not found in any of the following locations: %s", templateName, strings.Join(searchPaths, ", "))
}
