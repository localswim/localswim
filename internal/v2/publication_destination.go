package v2

import (
	"regexp"
	"strings"
)

type publicationSelection struct {
	remote string
	branch string
}

type publicationDestination struct {
	remote publicationRemote
	ref    string
}

func publicationConfigValue(cwd, query, key string, options ...string) (string, error) {
	args := append([]string{"config", "--includes", "--null"}, options...)
	data, err := publicationGit(cwd, "", nil, append(args, query, key)...)
	if gitConfigAbsent(err) && len(data) == 0 {
		return "", nil
	}
	text, ok := strings.CutSuffix(string(data), "\x00")
	if err != nil || !ok {
		return "", gitPublicationError("git_configuration_unavailable")
	}
	if strings.ContainsRune(text, 0) {
		return "", gitPublicationError("publication_target_ambiguous")
	}
	if text == "" {
		return "", gitPublicationError("publication_destination_unavailable")
	}
	return text, nil
}

func publicationConfigScalar(cwd, key string, options ...string) (string, error) {
	// Git's scalar lookup applies config precedence, including includes
	// and worktree overrides.
	return publicationConfigValue(cwd, "--get", key, options...)
}

func (repo publicationRepository) manualDestination(source string, selection publicationSelection) (publicationDestination, error) {
	list, err := publicationGitText(repo.root, "remote")
	if err != nil {
		return publicationDestination{}, gitPublicationError("git_configuration_unavailable")
	}
	if list == "" {
		return publicationDestination{}, gitPublicationError("remote_unavailable")
	}
	remotes := strings.Split(list, "\n")
	branchKey := "branch." + strings.TrimPrefix(source, "refs/heads/") + "."
	remote, err := repo.manualRemote(branchKey, selection.remote, remotes)
	if err != nil {
		return publicationDestination{}, err
	}
	defaultFetch := "origin"
	if len(remotes) == 1 {
		defaultFetch = remotes[0]
	}
	ref, err := repo.manualDestinationRef(source, branchKey, remote, defaultFetch, selection.branch)
	if err != nil {
		return publicationDestination{}, err
	}
	target, err := repo.resolvedPushURL(remote)
	if err != nil {
		return publicationDestination{}, err
	}
	return publicationDestination{remote: pinnedPublicationRemote(target), ref: ref}, nil
}

func (repo publicationRepository) manualRemote(branchKey, explicit string, remotes []string) (string, error) {
	name := explicit
	if name == "" {
		for _, key := range []string{branchKey + "pushRemote", "remote.pushDefault", branchKey + "remote"} {
			var err error
			name, err = publicationConfigScalar(repo.root, key)
			if err != nil {
				return "", err
			}
			if name != "" {
				break
			}
		}
	}
	if name == "" {
		if len(remotes) != 1 {
			return "", gitPublicationError("publication_target_ambiguous")
		}
		name = remotes[0]
	}
	// Git accepts URL or path arguments for a push. Only allow a named,
	// configured remote, with a name safe for use in Git config keys.
	if strings.HasPrefix(name, "-") || !repo.validGitRef("refs/remotes/"+name+"/HEAD") {
		return "", gitPublicationError("remote_unavailable")
	}
	for _, remote := range remotes {
		if name == remote {
			return name, nil
		}
	}
	return "", gitPublicationError("remote_unavailable")
}

func (repo publicationRepository) validGitRef(ref string) bool {
	_, err := publicationGit(repo.root, "", nil, "check-ref-format", ref)
	return err == nil
}

func (repo publicationRepository) manualDestinationRef(source, branchKey, remote, defaultFetch, explicit string) (string, error) {
	const heads = "refs/heads/"
	if explicit != "" {
		if !strings.HasPrefix(explicit, "refs/") {
			explicit = heads + explicit
		}
		return repo.publicationBranchRef(explicit)
	}
	key := "remote." + remote + "."
	mirror, err := publicationConfigScalar(repo.root, key+"mirror", "--type=bool")
	if err != nil {
		return "", err
	}
	if mirror == "true" {
		return "", gitPublicationError("publication_target_ambiguous")
	}
	refspec, err := publicationConfigValue(repo.root, "--get-all", key+"push")
	if err != nil {
		return "", err
	}
	if refspec != "" {
		from, to, exact := strings.Cut(refspec, ":")
		// An exact full destination ref is known without contacting the
		// remote. Never infer a remote branch from a bare refspec target.
		if !exact || strings.HasPrefix(from, "+") || (from != "HEAD" && from != source) {
			return "", gitPublicationError("publication_destination_unavailable")
		}
		return repo.publicationBranchRef(to)
	}
	mode, err := publicationConfigScalar(repo.root, "push.default")
	if err != nil {
		return "", err
	}
	switch mode {
	case "current":
		return source, nil
	case "matching":
		return "", gitPublicationError("publication_target_ambiguous")
	case "", "simple", "upstream", "tracking":
		return repo.manualUpstreamRef(source, branchKey, remote, defaultFetch, mode)
	default:
		return "", gitPublicationError("publication_destination_unavailable")
	}
}

func (repo publicationRepository) publicationBranchRef(ref string) (string, error) {
	if !strings.HasPrefix(ref, "refs/heads/") || !repo.validGitRef(ref) {
		return "", gitPublicationError("publication_destination_unavailable")
	}
	return ref, nil
}

func (repo publicationRepository) manualUpstreamRef(source, branchKey, remote, defaultFetch, mode string) (string, error) {
	upstream := mode == "upstream" || mode == "tracking"
	fetchRemote, err := publicationConfigScalar(repo.root, branchKey+"remote")
	if err != nil {
		return "", err
	}
	effectiveFetch := fetchRemote
	if effectiveFetch == "" {
		effectiveFetch = defaultFetch
	}
	if effectiveFetch != remote {
		if upstream {
			return "", gitPublicationError("publication_destination_unavailable")
		}
		// Git simple uses the local branch name when the push and fetch
		// remotes differ.
		return source, nil
	}
	merge, err := publicationConfigValue(repo.root, "--get-all", branchKey+"merge")
	if err != nil {
		return "", err
	}
	if merge != "" {
		if fetchRemote == "" || (!upstream && merge != source) {
			return "", gitPublicationError("publication_destination_unavailable")
		}
		return repo.publicationBranchRef(merge)
	}
	if mode == "" {
		// Retain publication's same-name default for a repository with no
		// tracking branch and no explicitly chosen push mode.
		return source, nil
	}
	autoSetup, err := publicationConfigScalar(repo.root, "push.autoSetupRemote", "--type=bool")
	if err != nil {
		return "", err
	}
	if autoSetup == "true" {
		// Use the branch implied by Git without changing tracking settings.
		return source, nil
	}
	return "", gitPublicationError("publication_destination_unavailable")
}

func (repo publicationRepository) resolvedPushURL(name string) (string, error) {
	// A synthetic remote preserves URL-scoped Git settings. It cannot
	// implicitly carry transport overrides scoped to the original name.
	transport := "^remote\\." + regexp.QuoteMeta(name) + "\\.(proxy|proxyauthmethod|receivepack|uploadpack|vcs|serveroption)$"
	_, err := publicationGit(repo.root, "", nil, "config", "--includes", "--name-only", "--get-regexp", transport)
	if err == nil {
		return "", gitPublicationError("unsupported_remote_transport")
	}
	if !gitConfigAbsent(err) {
		return "", gitPublicationError("git_configuration_unavailable")
	}
	data, err := publicationGit(repo.root, "", nil, "remote", "get-url", "--push", "--all", "--", name)
	target, ok := strings.CutSuffix(string(data), "\n")
	if err != nil || !ok || target == "" || strings.ContainsAny(target, "\r\x00") {
		return "", gitPublicationError("remote_unavailable")
	}
	if strings.Contains(target, "\n") {
		return "", gitPublicationError("publication_target_ambiguous")
	}
	return target, nil
}
