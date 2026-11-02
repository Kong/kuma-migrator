package migrator

import "fmt"

// RejectedByTargetError is returned by TransformDocumentWithOptions, together
// with the output documents, when the migrator knowingly emits a document the
// target control plane rejects: the conversion produced the best output it can,
// but applying it as-is would fail on that target.
//
// processFile writes such documents to ManualDir rather than the apply tree, and
// the report lists them with Reason, the same way it lists errored conversions.
type RejectedByTargetError struct {
	Target TargetVersion
	Reason string
}

func (e *RejectedByTargetError) Error() string {
	return fmt.Sprintf("rejected by --to-latest %s: %s", e.Target, e.Reason)
}
