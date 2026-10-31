package subentity

import "github.com/cto-up/core-lib/api/openapi/core"

type TenantProfile struct {
	DisplayName string           `json:"displayName"`
	Values      string           `json:"values"`
	LightColors core.ColorSchema `json:"lightColors"`
	DarkColors  core.ColorSchema `json:"darkColors"`
}
