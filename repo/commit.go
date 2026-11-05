package repo

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lvdousha26/minigit/object"
)

type Identity struct {
	Name  string
	Email string
	Unix  int64
	TZ    string
}

func (i Identity) Line(role string) string {
	return fmt.Sprintf("%s %s <%s> %d %s", role, i.Name, i.Email, i.Unix, i.TZ)
}

type Commit struct {
	Tree      string
	Parents   []string
	Author    string
	Committer string
	Message   string
}

// Identity resolves the author/committer in the same order Git does:
// environment, then .git/config, then a fixed placeholder.
func (r *Repo) NewIdentity(kind string) Identity {
	envName, envMail, envDate := "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_AUTHOR_DATE"
	if kind == "committer" {
		envName, envMail, envDate = "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "GIT_COMMITTER_DATE"
	}
	i := Identity{
		Name:  os.Getenv(envName),
		Email: os.Getenv(envMail),
		Unix:  time.Now().Unix(),
		TZ:    time.Now().Format("-0700"),
	}
	if i.Name == "" {
		i.Name = r.Config("user.name")
	}
	if i.Email == "" {
		i.Email = r.Config("user.email")
	}
	if i.Name == "" {
		i.Name = "minigit"
	}
	if i.Email == "" {
		i.Email = "minigit@localhost"
	}
	if raw := os.Getenv(envDate); raw != "" {
		// Accept the common "@<unix> <+hhmm>" form so tests can pin a date.
		parts := strings.Fields(strings.TrimPrefix(raw, "@"))
		if len(parts) >= 1 {
			if v, err := strconv.ParseInt(parts[0], 10, 64); err == nil {
				i.Unix = v
			}
		}
		if len(parts) >= 2 && len(parts[1]) == 5 {
			i.TZ = parts[1]
		}
	}
	return i
}

// WriteCommit serializes a commit object. The message is stored with exactly
// one trailing newline, matching what git commit-tree produces.
func (r *Repo) WriteCommit(tree string, parents []string, message string) (string, error) {
	if _, err := object.DecodeOID(tree); err != nil {
		return "", err
	}
	for _, p := range parents {
		if _, err := object.DecodeOID(p); err != nil {
			return "", err
		}
	}
	author := r.NewIdentity("author")
	committer := r.NewIdentity("committer")
	var b strings.Builder
	fmt.Fprintf(&b, "tree %s\n", tree)
	for _, p := range parents {
		fmt.Fprintf(&b, "parent %s\n", p)
	}
	b.WriteString(author.Line("author") + "\n")
	b.WriteString(committer.Line("committer") + "\n")
	b.WriteString("\n")
	b.WriteString(strings.TrimRight(message, "\n") + "\n")
	return r.Objects.Write(object.Commit, []byte(b.String()))
}

func (r *Repo) ReadCommit(oid string) (*Commit, error) {
	obj, err := r.Objects.Read(oid)
	if err != nil {
		return nil, err
	}
	if obj.Type != object.Commit {
		return nil, fmt.Errorf("%s is a %s, not a commit", oid, obj.Type)
	}
	return ParseCommit(obj.Data)
}

// ParseCommit reads the header block up to the first blank line; everything
// after it is the message, including any further blank lines.
func ParseCommit(data []byte) (*Commit, error) {
	text := string(data)
	head, msg, ok := strings.Cut(text, "\n\n")
	if !ok {
		return nil, fmt.Errorf("commit has no message separator")
	}
	c := &Commit{Message: msg}
	for _, line := range strings.Split(head, "\n") {
		key, val, found := strings.Cut(line, " ")
		if !found {
			continue
		}
		switch key {
		case "tree":
			c.Tree = val
		case "parent":
			c.Parents = append(c.Parents, val)
		case "author":
			c.Author = val
		case "committer":
			c.Committer = val
		}
	}
	if _, err := object.DecodeOID(c.Tree); err != nil {
		return nil, fmt.Errorf("commit has no valid tree: %w", err)
	}
	return c, nil
}
