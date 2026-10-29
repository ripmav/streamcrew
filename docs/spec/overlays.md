# Spezifikation: Overlays und Medien

| | |
|---|---|
| **Status** | Geprüft (abgenommen per Arbeitsauftrag des Projektinhabers vom 2026-10-27) |
| **Stand** | 2026-10-27 |
| **Bezug** | Roadmap Phase 7 (7.1–7.5), ADRs [0019](../adr/0019-overlay-architektur.md) und [0020](../adr/0020-audio-ausgabe.md), Plan §6.16–6.17; verwandte Spezifikationen [`command-engine.md`](command-engine.md) (B43), [`actions.md`](actions.md), [`events.md`](events.md) |
| **Umsetzung** | 7.1 `internal/overlay` + `internal/overlay/runtime`, 7.2 `internal/action/overlay` + Widget-Dienste, 7.3 `internal/media` + `internal/tts` + Actions `sound`/`text_to_speech`, 7.4 `internal/obs` + Action `streaming_software`, 7.5 Widget-Dienste |

## Zweck und Umfang

Diese Spezifikation beschreibt das Verhalten des Overlay-Servers, der Overlay-Runtime, der
Items und Widgets, der Medien-Ausgabe (Audio, TTS) und der OBS-Anbindung. Ausdrücklich nicht
dabei: die Editoren für Widgets und Endpunkte (Desktop-Track D5, Web-Track W4), der
Desktop-Modus des Agents (Phase 9.6) und die Währungen/Spiele, an deren Daten einige Widgets
anknüpfen (Phase 8).

## Begriffe

| Begriff | Bedeutung |
|---|---|
| Overlay-Endpunkt | Ein benannter Endpunkt mit eigener URL; eine OBS-Browserquelle lädt genau eine dieser URLs (Q1) |
| Item | Eine einmalige Ausgabe auf einem Endpunkt (Text, Bild, Video, Ton, HTML, YouTube, Clip, Timer, Emote-Effekt), die nach ihrer Laufzeit verschwindet (Q2) |
| Widget | Ein persistenter, im Profil definierter Element mit Zustand auf einem Endpunkt (Q3) |
| Wiedergabe-Ende | Der Moment, zu dem die Wiedergabe von Ton oder Video endet; die Aktion meldet ihn mit `engine.Run.PlaybackEnds` (Roadmap 7.2, `command-engine.md` B43) |
| Sink | Der Ort, an dem Audio wiedergegeben wird: `overlay` (Browserquelle) oder `local` (Gerät des Hosts) (ADR-0020) |

## Verhalten

### Server, Endpunkte, Transport (7.1)

| ID | Regel | Quellen |
|---|---|---|
| B1 | Der Core betreibt einen eigenen HTTP-Server für Overlays neben dem API-Server. Der lauschende Port ist Startkonfiguration (`overlay-listen`); Defaults je Betriebsmodus wie `listen`: `127.0.0.1:8742` (Daemon/Desktop), `:8742` (Server). Der Port ändert sich nur mit Neustart. | Q1, Q5, Q9 |
| B2 | Overlay-Endpunkte sind benannte Endpunkte im Profil (Settings-Sektion "overlay", Feld `endpoints`, je Endpunkt `id` und `name`). Die Id `default` existiert immer. Je Endpunkt gibt es genau eine URL der Form `http(s)://<host>:<port>/overlay/<id>`; unbekanntes `<id>` antwortet mit 404. | Q1 |
| B3 | Die URL liefert die Overlay-Runtime (HTML+JS, eingebettet). Über `…/overlay/<id>/ws` verbindet sich die Runtime per WebSocket. Pakete sind JSON-Objekte `{type, data}` (ADR-0019). | Q1, Q5 |
| B4 | Beim (Wieder-)Verbinden sendet der Server in dieser Reihenfolge: `hello` (Server-Version, Endpunkt, Serverzeit), `state.widgets` (alle sichtbaren Widgets des Endpunkts mit Zustand), dann die aktuell aktiven Items neu als `item.show`. Danach laufen nur noch Änderungen. | Q1, Q4 |
| B5 | Page, WebSocket und Medien-URLs nehmen einen API-Token des Profils mit dem Scope `overlay` als Query-Parameter `token` (ADR-0013 §5, ADR-0019 §1). Ohne Token sind die Endpunkte nur im Desktop- und Daemon-Modus bei reinem Loopback-Bind und mit einem `Host`-Header, der einen Loopback-Namen nennt, erreichbar (Schutz gegen DNS-Rebinding); sonst, bei einem Token ohne Scope `overlay` und bei einem ungültigen oder widerrufenen Token antworten sie mit 401. Der Scope `overlay` öffnet nur diese Endpunkte, nicht die API. Die Endpunkte sind nur lesend. | Q1, Q5, Q6 |
| B6 | Medien (Bilder, Videos, Ton) werden nur aus freigegebenen Verzeichnissen ausgeliefert: dem Medien-Verzeichnis des Profils (`media`) und den freigegebenen Wurzeln (`file-root`). Die URL-Form ist `/media/<wurzel>/<pfad>`; die Lieferung nutzt `http.ServeContent` (Range-Requests) und korrekte MIME-Typen. Ohne das Recht `host:fs` (Server-Modus) liefert der Server keine Medien (404). | Q1, Q6 |
| B7 | Die Runtime verbindet sich automatisch und wieder mit Backoff (1 s bis 30 s), wenn die Verbindung abreißt; nach der (Wieder-)Verbindung stellt sie sich mit `ready` vor und rendert den empfangenen Zustand. | Q1, Q4 |
| B8 | Aufeinanderfolgende Items desselben Endpunkts, die sich zeitlich überlappen würden, blendet die Runtime versetzt ein (Batching): ein Item, das innerhalb von 500 ms nach dem Vorherigen gezeigt wird, startet 300 ms später als dieses. | Q5 |
| B9 | Items und Widgets haben Position und Ebene: Positionen sind `simple` (neun Bildschirmpunkte), `percent` (x/y in Prozent), `pixels` (x/y in Pixeln) oder `random` (Zufallspunkt im Pixel-Bereich, vom Server beim Zeigen gewürfelt). Ebenen sind ganze Zahlen; höhere Zahlen liegen darüber. | Q2 |
| B10 | Items haben Ein- und Ausblend-Animationen aus dem Satz `none`, `fade`, `slide-left`, `slide-right`, `slide-up`, `slide-down`, `pop` mit eigener Dauer (Millisekunden, Default 300). Die Animation läuft als CSS-Übergang in der Runtime. | Q2, Q5 |
| B11 | Ein Item verschwindet nach seiner Laufzeit: der Server sendet `item.remove` nach der Dauer (Default 10 s) und streicht es aus dem aktiven Satz. Video- und Ton-Items ohne gesetzte Dauer laufen, bis die Wiedergabe endet (B14), maximal bis zum Zeitlimit der Aktion. | Q2 |
| B12 | HTML-Items rendert die Runtime in einem sandboxed iframe ohne Skript- und ohne same-origin-Zugriff; das HTML darf nur relative `/media/`- und absolute HTTPS-URLs referenzieren. | Q2, Q6 |

### Items (7.2)

| ID | Regel | Quellen |
|---|---|---|
| B13 | Item-Kinds: `text` (Text, optionaler Max-Width für Zeilenumbruch), `image` (Pfad oder URL, optionale Breite/Höhe, nur eine Angabe → proportionale Skalierung), `video` (Pfad oder URL, optionale Dauer, Startzeit in Sekunden, Lautstärke 0–100, Schleife), `audio` (Pfad oder URL, Dauer, Lautstärke), `html` (eigenes HTML), `youtube` (URL, Breite und Höhe zwingend, Startzeit, Lautstärke), `clip` (Twitch-Clip: URL oder letzter Clip eines Users), `timer` (Dauer, aufwärts oder abwärts zählen, Anzeigeformat), `emote_effect` (7.5). | Q2 |
| B14 | Ton- und Video-Items melden ihr Wiedergabe-Ende: die Runtime sendet `item.ended`, wenn der Browser die Wiedergabe beendet hat. Die Core-Aktion meldet das Ende an die Engine mit `engine.Run.PlaybackEnds` (B43); ohne Meldung zählt die Engine vom Ende der Aktion. | Q2, Q7 |
| B15 | `youtube` und `clip` laden als Embeds in der Browserquelle (YouTube: Embed-Frame; Clip: Clip-Seite in einem Frame). Brechen sie ohne Netz, bleibt das Overlay ansonsten funktionsfähig. | Q2 |
| B16 | Der Clip-Item zeigt die Dauer des Clips als Detail im Bus-Ereignis, falls ein Event-Command sie braucht (Analogon zu `$twitchclipduration`, [Interop]). | Q2 |
| B17 | `timer` zählt als Item eine Dauer abwärts (Standard) oder aufwärts und zeigt das Format `DD`, `HH`, `hh`, `MM`, `mm`, `SS`, `ss` (kumuliert bzw. Rest, B28). | Q2, Q8 |
| B18 | Das Item zeigt sich auf dem in der Aktion genannten Endpunkt (Default `default`); existiert der Endpunkt nicht, ist das ein Fehler der Aktion mit klarer Meldung. | Q1 |

### Widgets: Definition und Zustand (7.2, 7.5)

| ID | Regel | Quellen |
|---|---|---|
| B19 | Widgets sind in der Settings-Sektion "overlay" (Feld `widgets`) definiert: je Widget `id` (eindeutig im Profil), `kind`, `name`, `endpoint` (Default `default`), `visible`, Position (B9) und eine `kind`-spezifische `config`. Mehrere Widgets derselben Art sind erlaubt. | Q3 |
| B20 | Der veränderliche Zustand eines Widgets (Fortschritt, Timer, Boss, Rad, Credits, Queue) speichert der Core im Profil (`overlay_widget_state`, ein JSON-Dokument je Widget). Ein Neustart ändert nichts am Zustand; ein Neuaufbau des Profils startet bei Default. | Q3, Q8, Q10 |
| B21 | `widget.show`/`widget.hide` (Aktion oder Profil-Änderung) schalten ein Widget ein/aus. Ein ausgeblendetes Widget läuft nicht mit (kein Tracking, keine Ereignisse) und wird nicht gesendet. | Q3 |
| B22 | `widget.update` ändert die Anzeige- und Zustandsdaten eines Widgets; die Runtime zeigt den neuen Zustand ohne Reload. | Q3 |
| B23 | Statt der "Widget Commands" des Originals publiziert der Core für jede Widget-Wirkung ein Bus-Ereignis mit Details; Event-Commands reagieren darauf (Abweichung A2). | Q3, Q8, Q10–Q13 |

### Widgets: Arten (7.2 P1, 7.5 P2)

| ID | Regel | Quellen |
|---|---|---|
| B24 | `label`: zeigt Text. `text` ist ein Template (Identifier erlaubt), das der Core alle 30 s neu rendert und bei Ereignis-Änderungen sofort, bei Textwechsel schickt. Arten `viewer_count`, `chatter_count`, `latest_follower`, `total_followers`, `latest_subscriber`, `total_subscribers`, `latest_raid` liefern den Wert aus Plattform- und Feed-Daten; `date` und `time` rendert die Runtime local (pro Sekunde) nach dem Locale des Profils. | Q14 |
| B25 | `goal`: Fortschrittsanzeige mit Segmenten (mindestens eins, `individual` oder `cumulative`). Fortschritt wächst über Ereignis-Beträge (`follow`, `subscribe`, `raid` additiv; `bits` additiv; `donation` multiplikativ — ab Phase 9) und über die Operation `add_to_goal` (Betrag, negativ erlaubt). Beim Erreichen eines Segments läuft dessen Command, sonst der Widget-Command `segment_completed` (nie beide); dazwischen das Ereignis `overlay.goal.progress` (Details: Gesamtbetrag, Teilbetrag, Segment-Name und -Betrag) bzw. `overlay.goal.segment_completed` (Details inkl. nächstem Segment und Rest). Optionaler Automatik-Reset (täglich/wochenmonatlich) setzt auf 0. | Q11 |
| B26 | `timer` (Widget): zählt eine gesetzte Dauer ab; wird über `widget.update` (neue Dauer, Reset, Pause) gesteuert, ist nicht persistiert; das Anzeigen rendert clientseitig pro Sekunde (Serverzeit aus `hello` als Bezug). | Q3 |
| B27 | `event_list`: Liste der letzten Ereignisse des Feeds (Typen wählbar, Default alle Feed-Typen) plus dynamische Einträge über die Operation `add_to_event_list` (User des Aufrufs + Details-Text); älteste Einträge fallen bei Erreichen der Maximalanzahl (Default 10) heraus. | Q12 |
| B28 | `chat`: zeigt die Chat-Nachrichten des Profils (Plattformen wählbar, Default alle). Optionen: Verzögerung (Millisekunden) vor der Anzeige, Entfernen nach gesetzter Zeit (0 = immer sichtbar), Maximalanzahl (Default 20). Gelooschte oder moderationsbetroffene Nachrichten verschwinden sofort. | Q13 |
| B29 | `persistent_timer` (7.5): zählt permanent ab; Zeit wird über Ereignis-Beträge (additiv, z. B. `follow`; multiplikativ, z. B. `donation` — ab Phase 9) und die Operation `add_to_timer` (negativ erlaubt) geändert. Persistiert (B20). Option `disable_on_completion` blendet bei 0 aus, `reset_on_enable` setzt bei Ausblenden und beim Herunterfahren auf den Anfangswert (Stream-a-thons: aus), `allow_adjustment_while_paused` erlaubt Änderungen in der Pause. Anzeigeformat `DD`/`HH`/`hh`/`MM`/`mm`/`SS`/`ss` (B17). Ereignis `overlay.timer.adjusted` (Details: geänderter Betrag) bei jeder Änderung, `overlay.timer.completed` bei Erreichen von 0. | Q8 |
| B30 | `stream_boss` (7.5): zeigt einen Boss (einen Channel-Nutzer) mit Gesundheit. Konfiguration: `base_health`, `kill_bonus_health`, `overkill_bonus_multiplier`, `self_heal_multiplier`, `compound_previous_health` (Default an). Ereignisse (`follow`, `subscribe`, `raid` additiv; `bits` additiv; `donation` multiplikativ — ab Phase 9) und die Operation `damage_stream_boss` (Betrag; negativ = Heilung; Aufrufer ist selbst der Boss → Heilung mit `self_heal_multiplier`, `force_damage` erzwingt Schaden) wirken auf den Boss. Bei 0 oder weniger Gesundheit: neuer Boss (zufälliger aktiver Nutzer, anders als der getötete), Gesundheit = (kompoundiert: bisherige Maximal-Gesundheit, sonst `base_health`) + `kill_bonus_health` + Overkill × `overkill_bonus_multiplier`. Ereignisse `overlay.boss.damage` / `overlay.boss.heal` (Details: Gesundheit, Betrag, User) und `overlay.boss.new` (Details: Gesundheit). | Q10 |
| B31 | `end_credits` (7.5): sammelt **nur während das Widget einblendet** je Stream-Sitzung Daten in Sektionen: `chatters`, `new_followers`, `new_subscribers`, `resubscribers`, `gifted_subscriptions`, `all_subscriptions`, `raids`, `twitch_bits` und beliebig viele `custom`-Sektionen (Name). Die Operation `add_to_credits` (Sektion + Text) fügt Custom-Einträge hinzu; `play_credits` startet die Abfolge: der Server schickt die Sektionen, die Runtime blendet sie rollend ein. Neustart der Sitzung (Stream-Start) leert die Daten; BANNte Nutzer werden entfernt. | Q9 |
| B32 | `leaderboard` (7.5): Top-N (Default 10) nach einem Statistiktyp aus den Nutzer-Statistiken (`watch_time`, `messages`, `commands`); bei Veränderung (30-s-Takt oder Ereignis) aktualisiert der Core die Liste live. | Q15 |
| B33 | `wheel` (7.5): Räder mit Outcomes; `equal_probability` (Default) oder je Outcome `probability` (Dezimalprozent, Summe 100). Optional je Outcome `modifier` (ganze Prozentpunkte, Summe aller Modifier = 0): trifft ein Outcome mit negativem Modifier, verschieben sich alle Wahrscheinlichkeiten um die Modifier; mit positivem Modifier werden sie zurückgesetzt; bei 0 ändert sich nichts. Der Spin (Operation `spin_wheel`) würfelt serverseitig, sendet die Drehung an die Runtime (Dauer, Ziel-Index) und publiziert `overlay.wheel.spun` (Details: Outcome-Name, User); läuft das Command des Outcomes, sonst das Widget-Command `default_command`. | Q16 |
| B34 | `poll` (7.5): zeigt die aktuell aktive Twitch-Abstimmung bzw. -Vorhersage (Name, Optionen, laufende Stimmen; bei Vorhersagen optional Punkte-Einsätze und Farben), live bei Fortschritts-Ereignissen, bis sie abgeschlossen ist. Eigene Chat-Abstimmungen (Bet/Trivia) zeigen sich ab Phase 8. | Q17 |
| B35 | `emote_effect` (Item, 7.5) und `persistent_emote_effect` (Widget, 7.5): zeigen Emotes mit Effekten (`rain`, `burst`, `random`). Konfiguration: Dauer, Breite, Höhe, `per_emote_shown` (Anzahl je vorkommendem Emote), `max_shown` (Gesamtdeckel), `allow_emoji`. Das Widget beobachtet zusätzlich Chat-Nachrichten und löst den Effekt erst ab einer Combo aus (`combo_count` gleiche Emotes innerhalb von `combo_timeframe` Sekunden; `ignore_duplicates` zählt je Emote pro Nachricht nur einmal). Emote-Namen löst der Core über die Emote-Kataloge in Bild-URLs auf; nicht gefundene Namen entfallen. | Q2, Q18 |
| B36 | `game_queue` (7.5): die Game-Queue des Profils (Beitritt/Verlassen/Position, max. Größe aus der Widget-Konfiguration, Default 10) wird persistiert (B20) und das Widget zeigt die aktuelle Liste live. Ereignisse `gamequeue.joined` / `gamequeue.left` / `gamequeue.position` (Details: User, Position). Vorrang für Abonnenten und die GameQueue-Action folgen in Phase 8.3. | Q19 |
| B37 | `discord_voice` (7.5, P3): mit gesetztem `discord-token` verbindet sich der Core über das Discord-Gateway; für den konfigurierten Sprachkanal (`discord-voice-channel`) veröffentlicht er `discord.voice.joined` / `discord.voice.left` (Details: Discord-Name, Kanal) und das Widget listet die aktuellen Besetzer. Ohne Token bleibt der Dienst inaktiv (Info-Log). **Beschränkung:** ohne Voice-Verbindung wird nicht erkannt, wer spricht (Abweichung A3). | Q20, Q6 |

### Actions (7.2, 7.3, 7.4)

| ID | Regel | Quellen |
|---|---|---|
| B38 | Die Overlay-Action (Typ `overlay`) hat die Kinds `item` (ein Item zeigen, Konfiguration nach B13/B9/B10, Endpunkt, Ebene) und `widget` (Operation: `show`, `hide`, `update` (Daten), `add_to_goal`, `add_to_timer`, `pause_timer`, `add_to_event_list`, `add_to_credits`, `play_credits`, `spin_wheel`, `damage_stream_boss`; je Operation das Ziel-Widget). Position und Animation liegen als Schema vor, damit Editoren sie generisch bearbeiten können. | Q2, Q3 |
| B39 | Die Overlay-Action ist `visual_audio` (Sperrmodus `visual_audio`, `command-engine.md` B23), solange das Item Ton oder Video trägt; die Action meldet Wiedergabe-Enden mit `PlaybackEnds` (B14). | Q2, Q7 |
| B40 | Die Sound-Action (Typ `sound`) spielt eine Datei (Pfad unter den freigegebenen Wurzeln) mit Lautstärke 0–100 auf dem Sink `overlay` (Default) oder `local` ab. `wait_for_finish` hält die Aktion bis zum Wiedergabe-Ende; immer meldet sie das Ende mit `PlaybackEnds`. Ohne Sink (lokal nicht gebaut, `host:audio` fehlt im Server-Modus) ist sie ein klarer Fehler. | Q21, Q22 |
| B41 | Die TextToSpeech-Action (Typ `text_to_speech`) spricht Text (Template) über den Provider `browser` (Default), `google`, `azure` oder `piper` mit Stimme, Lautstärke, Sprechrate und Tonhöhe; `wait_for_finish` hält die Aktion bis zum Ende (Default aus), der Sink wie B40. Cloud-Provider lesen ihre Zugangsdaten aus Umgebungsvariablen (`STREAMCREW_TTS_GOOGLE_KEY`; `STREAMCREW_TTS_AZURE_KEY`/`_REGION`), die nie in Logs erscheinen; `piper` läuft als externer Prozess und braucht `host:process`. | Q22, Q23, Q24 |
| B42 | Die StreamingSoftware-Action (Typ `streaming_software`) hat die Kinds `set_scene`, `start_stream`, `stop_stream`, `start_recording`, `stop_recording`, `save_replay`, `set_source` (Sichtbarkeit und/oder Position einer Quelle der aktuellen Szene) und `set_filter` (Sichtbarkeit eines Filters einer Quelle). Ohne aktive OBS-Verbindung ist jede Ausführung eine klare Fehlermeldung (kein Stillstand ohne Hinweis). | Q25, Q26 |

### OBS-Events (7.4)

| ID | Regel | Quellen |
|---|---|---|
| B43 | Der Core verbindet sich zu OBS (obs-websocket v5) mit `obs-host`/`obs-port`/`obs-password` (Defaults `127.0.0.1:4455`, ohne Passwort). Schlägt die Verbindung beim Start fehl, gibt der Core sie auf (keine Retries, Info-Log); bricht eine zuvor bestandene Verbindung ab, verbindet er sich alle 5 s neu. | Q25 |
| B44 | Diese OBS-Ereignisse laufen als Bus-Ereignisse (Details in Klammern), unabhängig davon, wer die Änderung herbeiführt: `obs.stream.started`/`obs.stream.stopped`, `obs.recording.started`/`obs.recording.stopped` (Dateipfad), `obs.replay.saved` (Dateipfad), `obs.scene.changed` (aktuelle und vorherige Szene), `obs.transition.started`/`obs.transition.ended` (Übergangsname), `obs.source.visibility_changed` (Szene, Quelle, sichtbar), `obs.filter.visibility_changed` (Quelle, Filter, sichtbar). Startende/stoppende Zwischenzustände von Stream und Aufnahme werden ignoriert (einmal je Änderung). | Q25 |

## Randfälle

| ID | Situation | Erwartetes Verhalten | Quellen |
|---|---|---|---|
| B45 | Item- oder Widget-Bezug auf einen nicht existierenden Endpunkt | Fehler der Aktion mit der Endpunkt-Id in der Meldung; Core läuft weiter | B18, Q1 |
| B46 | Wiedergabe-Ende ohne Client-Meldung (Browserquelle wird geschlossen) | Die Aktion wertet das Ende zum Zeitlimit als beendet; `PlaybackEnds` bleibt auf der Schätzung | B11, B14, Q7 |
| B47 | Medien-Pfad außerhalb der freigegebenen Wurzeln (Punkt-Slash, absolute Pfade) | Kein Zugriff (os.Root), 404; die Aktion lehnt das Speichern/Zeigen mit klarem Fehler ab | B6, Q6 |
| B48 | Token gesetzt, Client ohne Parameter | 401 für Page, WS-Upgrade und Medien; im Log eine Warnung mit Adresse, ohne Token-Wert | B5 |
| B49 | Zwei Clients (zwei Browserquellen) am selben Endpunkt | Beide sehen denselben Stand; `item.ended` zählt, wenn der erste Client es meldet | B4 |
| B50 | Goal erreicht genau durch einen negativen `add_to_goal` unter 0 | Fortschritt wird auf 0 geklemmt; Segment-Wirkungen laufen nur in aufsteigender Richtung | B25 |
| B51 | Wheel-Spin mit Summe der Wahrscheinlichkeiten ≠ 100 | Speichern/Update lehnt ab (klare Meldung) | B33 |
| B52 | Stream-Boss bei leerem Kanal | Der erste Boss ist der Streamer; wird er "getötet", bleibt er Boss, bis ein anderer aktiver Nutzer da ist (sonst Neuwahl auf den Streamer) | B30 |
| B53 | End-Credits-Sammlung bei Widget-Aus | Keine Sammlung; bestehende Daten bleiben bis zum nächsten Play/Reset | B31 |
| B54 | TTS-Cloud-Provider ohne gesetzten Key | Klare Fehlermeldung der Aktion ("kein Key"), kein Crash, keine Key-Ausgabe im Log | B41 |
| B55 | OBS-Verbindung während eines laufenden Commands | Die OBS-Action wartet nicht: fehlende Verbindung = sofortiger Fehler des Aktions-Schritts (Fehlerpolitik des Commands greift) | B42, Q25 |

## Abweichungen vom Original

| ID | Original | streamcrew | Begründung |
|---|---|---|---|
| A1 | Widget- und Endpunkt-Editor in der Desktop-App | Kopflos definiert über die Settings-Sektion und (später) die API; Editoren kommen mit D5/W4 | Kein GUI-Pfad in der Phase 7; die Sektion ist der gleiche Datenpfad |
| A2 | "Widget Commands" (Commands, die ein Widget direkt ausführt) | Bus-Ereignisse (`overlay.*`), auf die Event-Commands reagieren | Gleiches Verhalten über die bestehende Event-Command-Mechanik, ohne zweiten Trigger-Pfad |
| A3 | Discord Reactive Voice reagiert auf Sprechaktivität im Voice-Kanal | Gateway-Bot ohne Voice-Verbindung: nur Kanal-Mitgliedschaft (join/leave) | Opus-Voice-Decoding ist außerhalb des P3-Umfangs; die Ereignisse und das Widget decken die sichtbare Basis ab |
| A4 | `Run Widget Function` (JS-Funktion des Widgets aufrufen) | entfällt | Widgets tragen kein eigenes ausführbares JS (Sandbox, B12); die Operation gibt eine klare Fehlermeldung |
| A5 | Label-Datum/Zeit wird pro Sekunde im Browser rendert, Label-Texte minutengleich neu ausgewertet | Label-Text wird serverseitig (30 s + Ereignis), Datum/Zeit clientseitig pro Sekunde | Weniger Draht-Verkehr; gleiche Anzeige |
| A6 | End-Credits-Sektionen für Patreon/Streamloots/YouTube/Kick/Velora/VPZone/Power-Ups | Sektionen für die verfügbaren Feed-Typen (B31) + Custom | Die übrigen Plattformen und Dienste stehen nicht im MVP-Umfang (ADR-0004, Phase 9) |
| A7 | `$obsscenename` u. a. als Template-Identifier | Die Werte liegen als Details der `obs.*`-Ereignisse vor; Identifier folgen mit der Template-Erweiterung | Vermeidung eines zweiten Auslieferungspfads in Phase 7 |
| A8 | Endpoint-spezifisches eigenes HTML/CSS/JS (sorgt für `allow-scripts`) | entfällt | Widerspricht der Sandbox-Garantie (B12, Plan §6.15) |
| A9 | TTS-Provider Windows-TTS, ResponsiveVoice, TTS.Monster, TikTok, Edge, Amazon Polly | Provider: browser, google, azure, piper | Roadmap-Umfang (7.3); `browser` deckt die Gratis-Option ab |
| A10 | Sound-Ausgabe auf Windows-Geräten als Standard | Sink `overlay` als Default; `local` über Build-Tag | Der Server-Betrieb muss headless bleiben (ADR-0003, Plan §6.15) |

## Akzeptanzkriterien

- [ ] B1–B7: `GET /overlay/default` liefert die Runtime; der WS-Client bekommt `hello`, `state.widgets`, aktive Items; der Reconnect wiederholt den Handshake; der Token verweigert ohne/ mit falschem Wert (401); `/media/…` liefert Range-Requests und verweigert ohne `host:fs`
- [ ] B8–B12: ein Item erscheint mit Position/Ebene/Animation und verschwindet nach der Dauer; Batching versetzt sich überlappende Items; HTML-Items laufen in sandboxed iframes
- [ ] B13–B18: jedes Item-Kind rendert; Ton-/Video-Items melden `item.ended`; YouTube/Clip als Embed
- [ ] B19–B23: Widgets aus der Sektion erscheinen auf ihrem Endpunkt; `show`/`hide`/`update` wirken; der Zustand überlebt einen Neustart (persistent_timer, goal, boss, queue)
- [ ] B24–B37: jedes Widget-Kind zeigt live aktuelle Daten (Feed, Chat, Sitzungsstatistik, Twitch-Polls); die Ereignis-Wirkungen publizieren die `overlay.*`/`gamequeue.*`/`discord.voice.*`-Ereignisse mit Details
- [ ] B38–B42: die Actions `overlay`, `sound`, `text_to_speech`, `streaming_software` sind im Typkatalog mit Schema; `PlaybackEnds` erreicht die Engine (B43-Mindestabstand der Begrüßungen)
- [ ] B43–B44: OBS-Verbindung mit Reconnect; alle `obs.*`-Ereignisse laufen als Bus-Ereignisse
- [ ] M3-Exit: eine Browserquelle zeigt Alerts (Follow, Abo, Cheer) mit Bild, Ton und TTS; ein Ziel-Widget aktualisiert live; ein Command wechselt die OBS-Szene

## Offene Fragen

- ~~Lokale Audio-Dekodierung: MP3-Dekoder in reinem Go; CGO-Freundlichkeit von oto unter Linux.~~ Erledigt 2026-10-27: WAV-only, oto/v3 braucht kein cgo ([ADR-0020, Nachtrag](../adr/0020-audio-ausgabe.md#nachtrag-2026-10-27-umsetzung-73), Punkte 1 und 4).
- ~~Der Endpunkt-Token soll später über die API erzeugt werden.~~ Entschieden 2026-10-29: Die Endpunkte nutzen API-Tokens mit dem Scope `overlay` (B5); sie entstehen über `streamcrew token create` und die Token-API.

## Quellen

| ID | Art | Fundstelle | Notiz |
|---|---|---|---|
| Q1 | Doku | <https://mixitup.bot/docs/overlays> (abgerufen 2026-10-27) | Overlay-Endpunkte, Port, Test-Connection, lokale Dateien, Browserquelle |
| Q2 | Doku | <https://mixitup.bot/docs/actions/overlay-action> (abgerufen 2026-10-27) | Dynamische Visuals (Text, Bild, Video, YouTube, HTML, Timer, Clip, Emote-Effekt), Positionen, Animationen, Widget-Interaktionen |
| Q3 | Doku | <https://mixitup.bot/docs/overlays/overlay-widgets> (abgerufen 2026-10-27) | Widgets sind persistent, Mehrfachnutzung, dedizierte URLs |
| Q4 | Beobachtung | Mix It Up v1.8.x: Browserquelle lädt die Endpunkt-URL, die Seite meldet die Verbindung über WebSocket | State-Sync beim Reconnect ist für die OBS-Option "Refresh browser when scene becomes active" nötig |
| Q5 | Plan | [`plan.md`](../plan.md) §6.16, ADR-0019 | Eigener Server, Protokoll `{type, data}`, Batching, esbuild-`go:embed` |
| Q6 | Plan | [`plan.md`](../plan.md) §6.15, ADR-0013 | Overlay nur lesend, URL-Token, sandboxed iframes, Medien unter `os.Root`/`host:fs` |
| Q7 | Spezifikation | [`command-engine.md`](command-engine.md) B43, B72 | `PlaybackEnds`, Mindestabstand der Begrüßungs-Medien, Zeitlimit |
| Q8 | Doku | <https://mixitup.bot/docs/overlays/persistent-timer> (abgerufen 2026-10-27) | Persistenter Timer: Persistenz, Ereignis-Beträge, Pause, Display-Format, Widget-Commands |
| Q9 | Doku | <https://mixitup.bot/docs/overlays/end-credits> (abgerufen 2026-10-27) | End-Credits: Sektionen, nur bei Einblenden sammeln, Play, Custom, BANNte entfernen |
| Q10 | Doku | <https://mixitup.bot/docs/overlays/stream-boss> (abgerufen 2026-10-27) | Stream Boss: Health, Overkill, Self-Heal, Compound, Schaden je Ereignis, Widget-Commands |
| Q11 | Doku | <https://mixitup.bot/docs/overlays/goal> (abgerufen 2026-10-27) | Goal: Typen, Segmente (individual/cumulative), Ereignis-Beträge, Segment-Commands, Auto-Reset |
| Q12 | Doku | <https://mixitup.bot/docs/overlays/event-list> (abgerufen 2026-10-27) | Event List: Typen, Item-Template, dynamische Einträge |
| Q13 | Doku | <https://mixitup.bot/docs/overlays/chat> (abgerufen 2026-10-27) | Chat-Widget: Verzögerung, Entfernen, Plattformen, Moderationsfolgen |
| Q14 | Doku | <https://mixitup.bot/docs/overlays/label> (abgerufen 2026-10-27) | Label: Typen, Formate, Datum/Zeit clientseitig, 1-Minuten-Check |
| Q15 | Doku | <https://mixitup.bot/docs/overlays/leaderboard> (abgerufen 2026-10-27) | Leaderboard: Typen, Live-Update |
| Q16 | Doku | <https://mixitup.bot/docs/overlays/wheel> (abgerufen 2026-10-27) | Wheel: Outcomes, Wahrscheinlichkeiten, Modifier (Summe 0), Outcome-/Default-Commands |
| Q17 | Doku | <https://mixitup.bot/docs/overlays/poll> (abgerufen 2026-10-27) | Poll-Widget: Twitch-Polls/-Vorhersagen live, Bet/Trivia |
| Q18 | Doku | <https://mixitup.bot/docs/overlays/persistent-emote-effect> (abgerufen 2026-10-27) | Persistenter Emote-Effekt: Combo-Count + Timeframe, Per-Emote, Duplicates |
| Q19 | Doku | <https://mixitup.bot/docs/overlays/game-queue> (abgerufen 2026-10-27) | Game-Queue-Widget: zeigt die Queue, live bei Änderungen |
| Q20 | Doku | <https://mixitup.bot/docs/integrations/discord> (abgerufen 2026-10-27) | Discord-Anbindung, Voice-Kanal-Reaktionen |
| Q21 | Doku | <https://mixitup.bot/docs/actions/sound-action> (abgerufen 2026-10-27) | Sound-Action: Datei, Lautstärke 0–100, Overlay als Audio-Ausgabe (nur Default-Endpunkt) |
| Q22 | Doku | <https://mixitup.bot/docs/actions/text-to-speech-action> (abgerufen 2026-10-27) | TTS-Action: Provider, Lautstärke/Pitch/Rate, Wait For Finish, Random Voice, SSML |
| Q23 | Doku | <https://mixitup.bot/docs/integrations/google-cloud-tts> und <https://mixitup.bot/docs/integrations/microsoft-azure-speech> (abgerufen 2026-10-27) | Google: eigener API-Key; Azure: Region + Subscription-Key |
| Q24 | Doku | <https://mixitup.bot/docs/overlays/persistent-timer#display-format> (abgerufen 2026-10-27) | Display-Format-Tokens DD/HH/hh/MM/mm/SS/ss |
| Q25 | Doku | <https://mixitup.bot/docs/integrations/obs-studio> (abgerufen 2026-10-27) | OBS-Verbindung (Port/Passwort, Reconnect alle 5 s), OBS-Events mit Identifiern |
| Q26 | Doku | <https://mixitup.bot/docs/actions/streaming-software-action> (abgerufen 2026-10-27) | StreamingSoftware-Action: Szene, Quelle, Filter, Stream, Aufnahme, Replay |
| Q27 | Plattform-Doku | <https://obsproject.com/forum/resources/obs-websocket.725/> (obs-websocket v5, abgerufen 2026-10-27) | Protokoll v5, Identifizierung mit Passwort, Standardport 4455 |

## Änderungshistorie

| Datum | Änderung |
|---|---|
| 2026-10-27 | Erstfassung (autonome Arbeit Phase 7, Arbeitsauftrag des Projektinhabers) |
