# kay demo — records assets/demo.gif with charmbracelet/vhs.
#
# Prereqs: install vhs (https://github.com/charmbracelet/vhs), build kay,
# and register at least one server you can reach. Then run:
#
#   go build -o kay ./cmd/kay
#   vhs assets/demo.tape
#
# Tweak the alias below to one of your registered servers. The window is sized
# wide enough (~160 cols) to show the Overview's three-column responsive layout.

Output assets/demo.gif

Set Shell "bash"
Set FontSize 15
Set Width 1760
Set Height 860
Set Padding 20
Set Theme "Catppuccin Mocha"

# --- setup (hidden): build kay, put it on PATH, and anonymize output ---
# KAY_DEMO=1 masks host/user/alias + Docker/process/service names so nothing
# confidential (IPs, hostnames, service names) appears in the recording.
Hide
Type "go build -o ./kay ./cmd/kay && export PATH=$PWD:$PATH KAY_DEMO=1 && clear"
Enter
Sleep 3s
Show

# --- intro: what's registered ---
Type "./kay ls"
Enter
Sleep 2s

# --- launch the dashboard: responsive multi-column Overview ---
Type "./kay dashboard --alias dev-do --interval 2s"
Enter
Sleep 5s

# discover the keybindings overlay
Type "?"
Sleep 3s
Space

# customise the Overview panels (reorder / hide), then cancel
Type "o"
Sleep 2s
Type "JJ"
Sleep 1s
Escape
Sleep 1s

# tab through the views
Type "2"
Sleep 2s
Type "3"
Sleep 2s
Type "4"
Sleep 2s
Type "5"
Sleep 2s
Type "1"
Sleep 2s

Type "q"
Sleep 1s

# --- fleet overview + its help ---
Type "./kay fleet"
Enter
Sleep 4s
Type "?"
Sleep 2s
Space
Type "q"
Sleep 1s
