# Security policy

Waymark Remote controls presentations and moves files on other people's computers, so a security flaw matters. Thank you for reporting one privately.

## How to report

- **Preferred:** GitHub private vulnerability reporting. In this repository, open **Security › Report a vulnerability**. Only the maintainer sees the report.
- **Otherwise:** write to the contact address on [koulaxizis.gr](https://koulaxizis.gr/) with "Waymark security" in the subject.

Please do not open a public issue or pull request for a security flaw.

Include what you can: the version (Settings › Version, or the file name of the release), the system, the steps to reproduce, and what an attacker gains. A proof of concept helps but is not required.

## What happens next

- An answer within **7 days**, saying whether the report is confirmed.
- A fix as soon as it is ready, in a new release with a note in the changelog. The update notice in the app offers it to everyone who turned the check on.
- Public disclosure after the fix, or after **90 days** at the latest, agreed with you. You are credited unless you ask not to be.

## Scope

- The app in `app/` (Agent, Controller, the pairing, the encrypted channel, the file transfer, the update check)
- The browser Controller at `waymarked.site/remote/` and the Android app `site.waymarked.app`
- The relay (`app/relay-worker`, `waymark-relay`)
- The site waymarked.site

Out of scope: relays and copies other people run themselves, and flaws in third-party code that is already fixed upstream (please tell us if we ship an old version).

## Supported versions

Only the latest stable release gets security fixes. Waymark has no stable release yet; until then, fixes go into the next one.

## What reports may contain

Reports are used only to fix the flaw and are deleted one year after the issue is closed. Please do not send other people's personal data or presentations; describe them instead.
