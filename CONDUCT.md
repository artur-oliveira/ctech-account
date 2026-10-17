# Code of Conduct

## Our Pledge

We are committed to making participation in this project a respectful, inclusive, and harassment-free experience for everyone, regardless of age, body size, disability, ethnicity, gender identity, level of experience, nationality, personal appearance, race, religion, or sexual identity.

## Our Standards

**Expected behaviour:**

- Use welcoming and inclusive language
- Respect differing viewpoints and experiences
- Accept constructive criticism gracefully
- Focus on what is best for the project and its users
- Show empathy toward other contributors

**Unacceptable behaviour:**

- Harassment, insults, or derogatory comments in any form
- Publishing others' private information without explicit permission
- Trolling or deliberately disruptive conduct
- Any other conduct that would be inappropriate in a professional setting

## Scope

This Code of Conduct applies to all project spaces — including GitHub issues, pull requests, discussions, and any other venue where contributors represent the project.

## Enforcement

Instances of abusive, harassing, or otherwise unacceptable behaviour may be reported to the project maintainer at **dev.arturoc@gmail.com**. All complaints will be reviewed and investigated promptly and fairly. The maintainer is obligated to maintain confidentiality with regard to the reporter.

## Attribution

This Code of Conduct is adapted from the [Contributor Covenant](https://www.contributor-covenant.org), version 2.1.

Native OAuth clients use PKCE and exact registered reverse-domain callbacks. Never embed confidential client secrets in mobile binaries or grant first-party status through self-service registration.

## Service unlink and the code exchange

`POST /v1.0/token` (`authorization_code`) may answer `503` for a person who left a CTech product on their own and signs
in to it again, when telling the product they are back (`user.relinked`) fails. No token is minted and nothing is
cleared locally. Do not add a fallback that mints anyway: the product would still refuse the person. The check reads
`unlinked_services` on the already loaded user, so it costs nothing for everyone else.
