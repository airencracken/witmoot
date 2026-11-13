# Setting up Witmoot

Witmoot shares its first-run setup and account commands with the other
Comfyware apps. Set the service's public HTTPS origin and listen address, then
create the first owner in the browser or the terminal.

Run with `sudo`, installed commands find the service's data directory and act
as its account. For a portable install, run them as the account that owns the
data directory.

## Browser

```sh
sudo witmoot setup
```

This prints the setup address and a private key. Start the service, open
`/setup`, and enter the key with your username and password. From the host
itself you can use the loopback HTTP address; from anywhere else, use HTTPS.
The page works without JavaScript. Keep the key private; it never appears in
a URL or the startup log. Setup closes once any owner exists and never
replaces an account.

## Command line

```sh
sudo witmoot create-owner --username yourname
sudo witmoot set-password --username yourname
```

Both commands ask for the password twice with echo off. For automation, use
`--password-stdin` or `--password-file /private/path/password`; the file must
be a private regular file. `--data-dir DIRECTORY` picks a portable instance.
Passwords need at least 12 characters and at most 72 bytes. `create-owner`
never changes an existing account. `set-password` signs the account out
everywhere.

Run `witmoot help setup` for command help. Settings and invitations are in the
app once you sign in.
