// SPDX-License-Identifier: Apache-2.0

package review_test

import (
	"errors"
	"os"
)

func writeFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o644) }

func errorsAs[T any](err error, target *T) bool { return errors.As(err, target) }
