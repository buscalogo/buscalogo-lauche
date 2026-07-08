#!/bin/sh
# AppIndicator no Pop!_OS/GNOME Wayland precisa de X11 para exibir ícone no systray.
export GDK_BACKEND=x11
exec /opt/buscalogo/buscalogo-agent "$@"
