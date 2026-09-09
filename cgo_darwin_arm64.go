//go:build darwin && arm64

/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */

package talon

/*
#cgo LDFLAGS: -L${SRCDIR}/lib/darwin_arm64 -ltalon
#cgo LDFLAGS: -Wl,-rpath,${SRCDIR}/lib/darwin_arm64
#cgo LDFLAGS: -framework Security -framework CoreFoundation -liconv
*/
import "C"
