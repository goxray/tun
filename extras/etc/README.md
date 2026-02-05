## Setting up secrets directory
We'll add user to run the binary in separate environment and set up permissions. The process does not read the secrets file, systemd does, but we'll add owner for clarity.
```sh
$ sudo useradd --system xray
$ sudo install -d -m 700 -o root -g xray /etc/goxray/
$ sudo install -m 640 -o root -g xray extras/etc/goxray/template1.env /etc/goxray/  # edit template1.env with your vless url
```

## Installing systemd unit
With this parameterized systemd unit we can hold multiple xray configurations and enable/disable them as needed. Notice `template1` name as a parameter, without the `.env` extension. Expects the binary to be located at `/usr/local/bin/goxray_cli_linux_amd64` path.
```
$ sudo install -m 644 -o root -g root extras/etc/systemd/system/goxray_cli@.service /etc/systemd/system/
$ sudo systemctl enable goxray_cli@template1.service
```

## Installing AppArmor profile
This allowes to run the binary only with required access, operating on [MAC](https://en.wikipedia.org/wiki/Mandatory_access_control) principles. AppArmor protection is only active when executable is located at `@{exec_path}` paths. Written for latest Debian/Ubuntu.
```sh
$ sudo install -m 644 -o root -g root extras/etc/apparmor.d/goxray_cli /etc/apparmor.d/  # install AppArmor profile for executable
$ sudo apparmor_parser --add /etc/apparmor.d/goxray_cli                                  # confine profile for executable
```
