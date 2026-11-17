# Generates the screenshots and the GIF in this folder with VHS (https://github.com/charmbracelet/vhs).
#
#   vhs docs/demo.tape
#
# Point it at a DEMO Redmine (a throwaway instance with fake data), never at a real one: change
# REDMINE_URL below to the demo instance. The key file must contain the demo instance's key and
# have mode 600:
#   (umask 077; printf 'X-Redmine-API-Key: <demo key>\n' > /tmp/demo-key)
# The flow assumes the demo data described in the README (it opens the first issue of the list).

Output docs/demo.gif
Set Shell "bash"
Set FontSize 15
Set Width 1600
Set Height 900
Set Padding 12
Set Theme "Catppuccin Mocha"
Set Framerate 15
Env REDMINE_URL "https://demo.redmine.example"
Env REDMINE_HEADER_FILE "/tmp/demo-key"
Env REDMINE_TUI_LANG "en"
Hide
Type "redmine-tui --config /dev/null" Enter
Sleep 4s
Show
Sleep 1s
Screenshot docs/screenshot-list.png
Sleep 1s
Down 3
Sleep 600ms
Up 3
Sleep 600ms
Enter
Sleep 2s
Screenshot docs/screenshot-detail.png
Sleep 1s
Tab
Sleep 2s
Screenshot docs/screenshot-history.png
Sleep 1500ms
Tab
Sleep 1500ms
Tab
Sleep 1500ms
Escape
Sleep 2s
Type "N"
Sleep 1s
Up 2
Enter
Sleep 400ms
Down 2
Enter
Sleep 600ms
Down
Enter
Sleep 400ms
Down
Enter
Sleep 500ms
Down
Type@40ms "Add dark mode to the settings screen"
Sleep 300ms
Down
Enter
Sleep 500ms
Type@30ms "Users asked for a dark theme in the settings screen."
Sleep 300ms
Ctrl+S
Sleep 700ms
Down
Sleep 200ms
Enter
Sleep 600ms
Down
Sleep 300ms
Down
Sleep 300ms
Enter
Sleep 600ms
Down
Sleep 200ms
Enter
Sleep 400ms
Down 4
Enter
Sleep 500ms
Down
Type "2"
Sleep 800ms
Screenshot docs/screenshot-create.png
Sleep 1500ms
Escape
Sleep 1s
Type "y"
Sleep 2s
Type "q"
Sleep 500ms
