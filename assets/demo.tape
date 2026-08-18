# kay demo — records assets/demo.gif with charmbracelet/vhs.
#
# Prereqs: install vhs (https://github.com/charmbracelet/vhs), build kay,
# and register at least one server you can reach. Then run:
#
#   go build -o kay ./cmd/kay
#   vhs assets/demo.tape
#
# The window is sized wide enough (~160 cols) to show the Overview's
# three-column responsive layout.

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

# --- the console: bare `kay` opens the live fleet as home ---
Type "./kay"
Enter
Sleep 5s

# discover the full key map
Type "?"
Sleep 3s
Space
Sleep 1s

# drill into the selected host's dashboard (same connection, no re-handshake)
Enter
Sleep 5s

# customise the Overview panels (reorder / hide), then cancel
Type "o"
Sleep 2s
Type "JJ"
Sleep 1s
Escape
Sleep 1s

# tab through the dashboard views
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

# back to the fleet home
Type "q"
Sleep 2s

# run a command on the host without leaving the console
Type "x"
Sleep 1s
Type "uptime"
Sleep 1s
Enter
Sleep 3s
Escape
Sleep 1s

# manage keys from the same screen
Type "K"
Sleep 3s
Escape
Sleep 1s

# leave the console
Type "q"
Sleep 1s
