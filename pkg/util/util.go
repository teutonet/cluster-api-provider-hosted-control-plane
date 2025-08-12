package util

import (
	"fmt"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	controllerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var titleCaser = cases.Title(language.English)

func OperationResultToTitledString(operation controllerutil.OperationResult) string {
	return titleCaser.String(fmt.Sprint(operation))
}
