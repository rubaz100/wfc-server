package cli

import (
	"os/exec"
	"runtime"
)

// openBrowser makes a best-effort attempt to open url in the user's default
// browser so a double-clicked launch lands straight on the Host Manager. Any
// failure is ignored: a headless or unusual environment simply keeps the
// printed URL in the console.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
