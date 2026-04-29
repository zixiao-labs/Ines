package typescript

import (
	"testing"

	"github.com/zixiao-labs/ines/internal/parser"
	"github.com/zixiao-labs/ines/internal/psi"
)

func TestTSBackendExtractsClassesAndMethods(t *testing.T) {
	src := []byte(`import { useState } from "react";

export class Counter {
	private value: number = 0;
	public increment(by: number = 1): number {
		this.value += by;
		return this.value;
	}
	get current(): number { return this.value; }
}

interface Repository<T> {
	get(id: string): Promise<T>;
}

export function add(a: number, b: number): number { return a + b; }

export const multiply = (a: number, b: number) => a * b;

type Id = string | number;
enum Status { Open, Closed }
`)
	backend := newTSBackend()
	tree, err := backend.Parse(parser.Source{Path: "demo.ts", Content: src, Language: "typescript"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got := map[string]psi.Kind{}
	for _, s := range tree.Symbols {
		got[s.Name] = s.Kind
	}
	expectations := map[string]psi.Kind{
		"react":      psi.KindImport,
		"Counter":    psi.KindClass,
		"Repository": psi.KindInterface,
		"add":        psi.KindFunction,
		"multiply":   psi.KindFunction,
		"Id":         psi.KindEnum,
		"Status":     psi.KindEnum,
	}
	for name, kind := range expectations {
		if got[name] != kind {
			t.Errorf("symbol %q: got %q want %q (all=%v)", name, got[name], kind, got)
		}
	}

	for _, s := range tree.Symbols {
		if s.Name == "Counter" {
			members := map[string]psi.Kind{}
			for _, c := range s.Children {
				members[c.Name] = c.Kind
			}
			if members["increment"] != psi.KindMethod {
				t.Errorf("Counter.increment kind: got %q", members["increment"])
			}
			if members["value"] != psi.KindField {
				t.Errorf("Counter.value kind: got %q", members["value"])
			}
		}
		if s.Name == "add" {
			names := []string{}
			for _, c := range s.Children {
				names = append(names, c.Name)
			}
			if len(names) != 2 || names[0] != "a" || names[1] != "b" {
				t.Errorf("add params: %v", names)
			}
		}
	}
}

func TestTSBackendHandlesArrowAssignment(t *testing.T) {
	src := []byte(`const greet = (name: string): string => "hi " + name;`)
	backend := newTSBackend()
	tree, err := backend.Parse(parser.Source{Path: "g.ts", Content: src, Language: "typescript"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(tree.Symbols) != 1 || tree.Symbols[0].Name != "greet" {
		t.Fatalf("greet missing: %+v", tree.Symbols)
	}
	if tree.Symbols[0].Kind != psi.KindFunction {
		t.Fatalf("greet should be promoted to function, got %q", tree.Symbols[0].Kind)
	}
}
