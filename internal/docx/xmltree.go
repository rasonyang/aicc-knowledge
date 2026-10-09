// SPDX-License-Identifier: Apache-2.0

package docx

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// node is a minimal DOM element. Names and attribute keys use local names only.
type node struct {
	name  string
	attrs map[string]string
	kids  []*node
	text  string
}

func (n *node) child(name string) *node {
	if n == nil {
		return nil
	}
	for _, k := range n.kids {
		if k.name == name {
			return k
		}
	}
	return nil
}

func (n *node) attr(name string) (string, bool) {
	if n == nil {
		return "", false
	}
	v, ok := n.attrs[name]
	return v, ok
}

const maxDepth = 512

func readTree(f *zip.File) (*node, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	lr := &io.LimitedReader{R: rc, N: maxPartBytes + 1}
	root, err := parseTree(lr)
	if err != nil {
		return nil, err
	}
	if lr.N <= 0 {
		return nil, errors.New("xml part exceeds size limit")
	}
	return root, nil
}

func parseTree(r io.Reader) (*node, error) {
	dec := xml.NewDecoder(r)
	var stack []*node
	var root *node
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if len(stack) >= maxDepth {
				return nil, errors.New("xml nesting too deep")
			}
			n := &node{name: t.Name.Local}
			if len(t.Attr) > 0 {
				n.attrs = make(map[string]string, len(t.Attr))
				for _, a := range t.Attr {
					n.attrs[a.Name.Local] = a.Value
				}
			}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.kids = append(p.kids, n)
			} else if root == nil {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text += string(t)
			}
		}
	}
	if root == nil {
		return nil, errors.New("empty xml document")
	}
	return root, nil
}

func cleanHeading(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
