// SPDX-License-Identifier: Apache-2.0

package store

import "io/fs"

func fsSub() (fs.FS, error) { return fs.Sub(migrationFS, "migrations") }
