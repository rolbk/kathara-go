package util

// GetCurrentUserHome is utils.get_current_user_home (utils.py:212) on Linux,
// where `exec_by_platform` picks the `passwd_home` arm (utils.py:220).
func GetCurrentUserHome() (string, error) {
	info, err := GetCurrentUserInfo()
	if err != nil {
		return "", err
	}
	return info.HomeDir, nil
}
