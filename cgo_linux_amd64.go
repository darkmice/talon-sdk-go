/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */
//go:build linux && amd64

package talon

/*
#cgo LDFLAGS: -L${SRCDIR}/lib/linux_amd64 -ltalon
#cgo LDFLAGS: -lm -ldl -lpthread
*/
import "C"
