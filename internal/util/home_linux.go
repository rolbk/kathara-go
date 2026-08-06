package util

// GetCurrentUserHome is utils.get_current_user_home (utils.py:212) on Linux,
// where `exec_by_platform` picks the `passwd_home` arm (utils.py:220).
//
// The three-way dispatch is asymmetric and it is not a mistake to preserve:
// Linux reads pw_dir out of the passwd entry, while macOS and Windows both get
// `os.path.expanduser('~')`. On Linux under sudo the two disagree — sudo
// rewrites `$HOME` to /root while [GetCurrentUserInfo] follows SUDO_UID back
// to the invoking user — so this returns the real user's home and mounts
// *that* as /hosthome (DockerMachine.py:311), which is the whole point of the
// SUDO_UID handling.
func GetCurrentUserHome() (string, error) {
	info, err := GetCurrentUserInfo()
	if err != nil {
		return "", err
	}
	return info.HomeDir, nil
}
