// External scanner for the GALA tree-sitter grammar: GALA's line-break rule.
//
// GALA has no statement terminator, so a line break matters only where a
// token could either continue the line before or begin a new statement. The
// GALA parser re-types those tokens (internal/parser/newline.go); this scanner
// does the same:
//
//   CALL_LPAREN  a '(' with no line break before it. Only a call takes it as
//                a call; every other '(' that can follow an expression takes
//                it too, so this token never forbids a parse that a plain '('
//                allows. After a line break the scanner declines, the
//                grammar's plain '(' is lexed, and the '(' begins a new
//                statement instead of calling the line before.
//   NL_STAR      a '*' after a line break, written directly against its
//   NL_AMP       operand ('*p', '&n'), where a prefix operator can begin. Only
//                the prefix operator takes it, so `*p = 5` on its own line is a
//                new statement; `* b` with a space still multiplies.
//
// A comment between the previous token and this one ends the scan: the
// grammar lexes the comment as an extra and the scanner runs again after it.

#include "tree_sitter/parser.h"

#include <stdbool.h>

enum TokenType {
  CALL_LPAREN,
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

bool tree_sitter_gala_external_scanner_scan(void *payload, TSLexer *lexer, const bool *valid_symbols) {
  (void)payload;

  // During error recovery every symbol is valid; let the internal lexer work.
  if (valid_symbols[ERROR_SENTINEL]) {
    return false;
  }
  if (!valid_symbols[CALL_LPAREN] && !valid_symbols[NL_STAR] && !valid_symbols[NL_AMP]) {
    return false;
  }

  bool newline = false;
  while (is_space(lexer->lookahead)) {
    if (lexer->lookahead == '\n') {
      newline = true;
    }
    lexer->advance(lexer, true);
  }

  switch (lexer->lookahead) {
  case '(':
    if (!valid_symbols[CALL_LPAREN] || newline) {
      return false;
    }
    lexer->advance(lexer, false);
    lexer->mark_end(lexer);
    lexer->result_symbol = CALL_LPAREN;
    return true;

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
