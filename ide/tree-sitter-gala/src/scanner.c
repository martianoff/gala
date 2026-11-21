// External scanner for the GALA tree-sitter grammar: GALA's line-break rule.
//
// GALA has no statement terminator, so a line break matters only where a
// token could either continue the line before or begin a new statement. The
// GALA parser re-types those tokens (internal/parser/newline.go); this scanner
// does the same:
//
//   CALL_LPAREN  a '(' with no line break before it. Only a call takes it as
//                a call; every other '(' takes it too, so this token never
//                forbids a parse that a plain '(' allows.
//   NL_LPAREN    a '(' after a line break. Everything but a call takes it, so
//                the '(' begins a new statement instead of calling the line
//                before.
//   NL_STAR      a '*' after a line break, written directly against its
//   NL_AMP       operand ('*p', '&n'), where a prefix operator can begin. Only
//                the prefix operator takes it, so `*p = 5` on its own line is a
//                new statement; `* b` with a space still multiplies.
//
// As in newline.go, a comment is not a token but a line break is a line
// break, including one inside a block comment. When a comment stands between
// the previous token and the next, the scanner reads through it to see both.
// If it then produces a token, the comment is part of that token's leading
// whitespace and is not in the tree; in every other case the scanner declines,
// and the grammar lexes the comment as usual.

#include "tree_sitter/parser.h"

#include <stdbool.h>

enum TokenType {
  CALL_LPAREN,
  NL_LPAREN,
  NL_STAR,
  NL_AMP,
  ERROR_SENTINEL,
};

void *tree_sitter_gala_external_scanner_create(void) { return NULL; }

void tree_sitter_gala_external_scanner_destroy(void *payload) { (void)payload; }

unsigned tree_sitter_gala_external_scanner_serialize(void *payload, char *buffer) {
  (void)payload;
  (void)buffer;
  return 0;
}

void tree_sitter_gala_external_scanner_deserialize(void *payload, const char *buffer, unsigned length) {
  (void)payload;
  (void)buffer;
  (void)length;
}

static bool is_space(int32_t c) { return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' || c == '\v'; }

// Skips whitespace and comments, and reports whether they hold a line break.
// Returns false if a '/' starts no comment: the scanner then declines.
static bool skip_space_and_comments(TSLexer *lexer, bool *newline, bool *comment) {
  for (;;) {
    if (is_space(lexer->lookahead)) {
      if (lexer->lookahead == '\n') {
        *newline = true;
      }
      lexer->advance(lexer, true);
      continue;
    }
    if (lexer->lookahead != '/') {
      return true;
    }
    lexer->advance(lexer, true);
    if (lexer->lookahead == '/') {
      while (lexer->lookahead != '\n' && !lexer->eof(lexer)) {
        lexer->advance(lexer, true);
      }
    } else if (lexer->lookahead == '*') {
      lexer->advance(lexer, true);
      for (;;) {
        if (lexer->eof(lexer)) {
          return false;
        }
        if (lexer->lookahead == '\n') {
          *newline = true;
        }
        if (lexer->lookahead == '*') {
          lexer->advance(lexer, true);
          if (lexer->lookahead == '/') {
            lexer->advance(lexer, true);
            break;
          }
          continue;
        }
        lexer->advance(lexer, true);
      }
    } else {
      return false;
    }
    *comment = true;
  }
}

bool tree_sitter_gala_external_scanner_scan(void *payload, TSLexer *lexer, const bool *valid_symbols) {
  (void)payload;

  // During error recovery every symbol is valid; let the internal lexer work.
  if (valid_symbols[ERROR_SENTINEL]) {
    return false;
  }
  if (!valid_symbols[CALL_LPAREN] && !valid_symbols[NL_LPAREN] && !valid_symbols[NL_STAR] &&
      !valid_symbols[NL_AMP]) {
    return false;
  }

  bool newline = false;
  bool comment = false;
  if (!skip_space_and_comments(lexer, &newline, &comment)) {
    return false;
  }
  // Without a line break the comment changes nothing: decline, so that the
  // grammar keeps it in the tree, and decide on the next call.
  if (comment && !newline) {
    return false;
  }

  switch (lexer->lookahead) {
  case '(': {
    const enum TokenType type = newline ? NL_LPAREN : CALL_LPAREN;
    if (!valid_symbols[type]) {
      return false;
    }
    lexer->advance(lexer, false);
    lexer->mark_end(lexer);
    lexer->result_symbol = type;
    return true;
  }

  case '*':
  case '&': {
    const bool star = lexer->lookahead == '*';
    if (!newline || !valid_symbols[star ? NL_STAR : NL_AMP]) {
      return false;
    }
    lexer->advance(lexer, false);
    lexer->mark_end(lexer);
    const int32_t next = lexer->lookahead;
    // Followed by whitespace or a comment it is a binary operator; `*=`,
    // `&&` and `&^` are other tokens.
    if (is_space(next) || next == '/' || next == 0 || next == '=' || (!star && (next == '&' || next == '^'))) {
      return false;
    }
    lexer->result_symbol = star ? NL_STAR : NL_AMP;
    return true;
  }

  default:
    return false;
  }
}
