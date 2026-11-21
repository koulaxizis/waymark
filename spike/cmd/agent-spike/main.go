// agent-spike is the presenter-PC side of the Waymark pairing spike.
// Throwaway code: it only answers ping and fake pres.* commands.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/koulaxizis/waymark/spike/internal/wm"
)

var (
	flagCode = flag.String("code", "", "flow A: Controller code (skips the menu)")
	flagFlow = flag.String("flow", "", "a or b (skips the menu)")
	flagYes  = flag.Bool("yes", false, "accept the 4-digit check without asking (automated tests only)")
	flagPage = flag.String("page", "https://waymarked.site/spike/pairing/", "phone page URL for flow B")
)

var lg *wm.Logger

func main() {
	flag.Parse()
	lg = wm.NewLogger("agent")
	defer lg.Close()
	lg.Header("agent", wm.SpikeVersion)

	flow := strings.ToLower(*flagFlow)
	if *flagCode != "" {
		flow = "a"
	}
	if flow == "" {
		fmt.Println()
		fmt.Println("Waymark Agent (spike)")
		fmt.Println("  1  Σύνδεση με κωδικό από Controller (υπολογιστής)   / Connect with a Controller code")
		fmt.Println("  2  QR για κινητό (Android, Chrome)                  / QR for a phone")
		switch wm.Ask("Επιλογή / Choice [1/2]: ") {
		case "2", "b", "B":
			flow = "b"
		default:
			flow = "a"
		}
	}
	var err error
	if flow == "b" {
		err = runFlowB()
	} else {
		err = runFlowA()
	}
	if err != nil {
		lg.Printf("ERROR %v", err)
	}
	if lg.Path != "" {
		lg.Say("\nΤο αρχείο log είναι στο / The log file is at:\n  %s\n", lg.Path)
	}
	if *flagCode == "" && *flagFlow == "" {
		wm.WaitExit()
	}
	if err != nil {
		os.Exit(1)
	}
}
