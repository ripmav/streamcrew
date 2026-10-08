# mixitup-go

Dies soll ein Port von `../mixitup` sein, der aber nicht C# nutzt sondern Go.
Architektonisch soll ein headless Core existieren, der dann von verschiedenen
Frontends genutzt werden kann. Das Frontend kann dann z.B. eine Weboberfläche
sein, eine CLI oder ein Desktop-Client.

CLI-Frontend wird direkt in das Headless-Core eingebunden, Desktop-Client und
Weboberfläche werden als separate Projekte entwickelt.

mixitup-go/mixitup-rebuild sind nur Arbeitsnamen und werden noch geändert.

## Wichtiges

- Entscheidungen werden als ADR (Architecture Decision Records) festgehalten
- ADRs werden in `docs/adr/` abgelegt
- ADRs werden in Dateien mit der Endung `.md` abgelegt
- ADRs werden in Markdown geschrieben
- ADRs werden in der Form `docs/adr/0001-<title>.md` abgelegt
- ADRs werden fortlaufend nummeriert. Die Nummerierung beginnt bei 0001.
- Code-ADRs werden in `docs/adr/code/` abgelegt.
- Code-ADRs werden in der Form `docs/adr/code/0001-<title>.md` abgelegt.
- Auch Entscheidungen bezüglich des Codes werden als ADR festgehalten.

## Roadmap

- [ ] Core
  - [ ] CLI-Frontend
- [ ] Desktop-Frontend
- [ ] Web-Frontend


## Core

### CLI

- Golang
- alecthomas/kong
- 

## Frontends

### CLI

- Golang
- alecthomas/kong
- charmbracelet/bubbletea

### Desktop

- Golang
- fyne
- fyne-cross
