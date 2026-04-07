# Documentation

## Put this in ~/.tmux.conf to allow shift+enter new lines
set -s extended-keys always
set -as terminal-features 'xterm*:extkeys'
bind-key -n S-Enter send-keys Escape "[13;2u"