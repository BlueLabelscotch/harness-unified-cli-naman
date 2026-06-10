// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package kg

import (
	_ "embed"

	"github.com/harness/harness-cli/pkg/registry"
)

//go:embed kg.help.txt
var helpText string

// ModuleInit registers kg workflows. Commands are declared in kg.spec.yaml.
func ModuleInit(reg registry.ModuleRegistrar) {
	reg.SetHelpText(helpText)
}
