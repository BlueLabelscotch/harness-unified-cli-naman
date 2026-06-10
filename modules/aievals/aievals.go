// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	_ "embed"

	"github.com/harness/harness-cli/pkg/registry"
)

//go:embed aievals.help.txt
var helpText string

// ModuleInit registers aievals workflows. Commands are declared in aievals.spec.yaml.
func ModuleInit(reg registry.ModuleRegistrar) {
	reg.SetHelpText(helpText)
}
