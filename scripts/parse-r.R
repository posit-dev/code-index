# Copyright (C) 2026 by Posit Software, PBC
# Licensed under the MIT License. See LICENSE for details.

#!/usr/bin/env Rscript
# parse-r.R — Parse R source files and extract functions, classes, and roxygen docs.
# Used by the code-index tool for accurate R code parsing.
#
# Usage: Rscript --vanilla scripts/parse-r.R <filepath>
# Output: JSON to stdout with functions and types arrays.

args <- commandArgs(trailingOnly = TRUE)

# Helper to write JSON output and exit.
emit_empty <- function() {
  cat('{"functions":[],"types":[]}\n')
  quit(save = "no", status = 0)
}

if (length(args) < 1) {
  emit_empty()
}

filepath <- args[1]

if (!file.exists(filepath)) {
  emit_empty()
}

# Ensure xmlparsedata is available.
ensure_xmlparsedata <- function() {
  if (requireNamespace("xmlparsedata", quietly = TRUE)) {
    return(TRUE)
  }
  # Try to install to a local library.
  lib_path <- file.path(tempdir(), "r-parse-lib")
  dir.create(lib_path, showWarnings = FALSE, recursive = TRUE)
  tryCatch({
    install.packages("xmlparsedata", lib = lib_path, repos = "https://cloud.r-project.org", quiet = TRUE)
    .libPaths(c(lib_path, .libPaths()))
    requireNamespace("xmlparsedata", quietly = TRUE)
  }, error = function(e) {
    return(FALSE)
  })
}

if (!ensure_xmlparsedata()) {
  emit_empty()
}

# Parse the file.
parsed <- tryCatch(
  parse(filepath, keep.source = TRUE),
  error = function(e) NULL
)

if (is.null(parsed)) {
  emit_empty()
}

# Convert to XML for structured traversal.
xml_str <- tryCatch(
  xmlparsedata::xml_parse_data(parsed, pretty = TRUE),
  error = function(e) NULL
)

if (is.null(xml_str) || nchar(xml_str) == 0) {
  emit_empty()
}

xml_doc <- tryCatch(
  xml2::read_xml(xml_str),
  error = function(e) {
    # xml2 might not be available; fall back without XML parsing.
    NULL
  }
)

# Read source lines for roxygen extraction.
source_lines <- readLines(filepath, warn = FALSE)

# Extract roxygen documentation above a given line.
extract_roxygen <- function(lines, func_line) {
  if (func_line <= 1 || func_line > length(lines)) return(list(doc = "", exported = FALSE))

  roxy_lines <- character(0)
  exported <- FALSE
  i <- func_line - 1

  while (i >= 1) {
    line <- trimws(lines[i])
    if (grepl("^#'", line)) {
      text <- sub("^#'\\s*", "", line)
      # Check for @export tag.
      if (grepl("^@export", text)) {
        exported <- TRUE
      } else if (grepl("^@param\\b|^@return\\b|^@returns\\b|^@examples\\b|^@importFrom\\b|^@import\\b|^@rdname\\b|^@seealso\\b|^@family\\b|^@inheritParams\\b|^@noRd\\b", text)) {
        # Skip other tags but keep scanning.
      } else if (grepl("^@title\\b", text)) {
        text <- sub("^@title\\s*", "", text)
        roxy_lines <- c(text, roxy_lines)
      } else if (grepl("^@description\\b", text)) {
        text <- sub("^@description\\s*", "", text)
        roxy_lines <- c(text, roxy_lines)
      } else if (!grepl("^@", text) && nchar(text) > 0) {
        roxy_lines <- c(text, roxy_lines)
      }
      i <- i - 1
    } else {
      break
    }
  }

  doc <- paste(roxy_lines, collapse = " ")
  if (nchar(doc) > 300) {
    doc <- paste0(substr(doc, 1, 300), "...")
  }

  list(doc = doc, exported = exported)
}

# Escape a string for JSON output.
json_escape <- function(s) {
  s <- gsub("\\\\", "\\\\\\\\", s)
  s <- gsub("\"", "\\\\\"", s)
  s <- gsub("\n", "\\\\n", s)
  s <- gsub("\r", "\\\\r", s)
  s <- gsub("\t", "\\\\t", s)
  s
}

functions <- list()
types <- list()

if (!is.null(xml_doc)) {
  ns <- xml2::xml_ns(xml_doc)

  # Find function assignments: name <- function(...) or name = function(...)
  # In the XML parse tree, these are LEFT_ASSIGN or EQ_ASSIGN with a
  # FUNCTION child.
  exprs <- xml2::xml_find_all(xml_doc, "//expr")

  for (expr in exprs) {
    children <- xml2::xml_children(expr)
    if (length(children) < 3) next

    child_names <- xml2::xml_name(children)

    # Pattern: expr (name) LEFT_ASSIGN/EQ_ASSIGN expr (containing FUNCTION)
    if (length(child_names) >= 3 &&
        child_names[1] == "expr" &&
        child_names[2] %in% c("LEFT_ASSIGN", "EQ_ASSIGN") &&
        child_names[3] == "expr") {

      # Check if the RHS expression contains a FUNCTION token.
      rhs <- children[[3]]
      rhs_children <- xml2::xml_children(rhs)
      rhs_names <- xml2::xml_name(rhs_children)

      if ("FUNCTION" %in% rhs_names) {
        # Get the function name from the LHS expression.
        lhs <- children[[1]]
        lhs_children <- xml2::xml_children(lhs)
        if (length(lhs_children) >= 1 && xml2::xml_name(lhs_children[[1]]) == "SYMBOL") {
          func_name <- xml2::xml_text(lhs_children[[1]])
          func_line <- as.integer(xml2::xml_attr(expr, "line1"))

          # Extract parameters from the formals.
          # Find the SYMBOL_FORMALS in the function expression.
          formals_nodes <- xml2::xml_find_all(rhs, ".//SYMBOL_FORMALS")
          params <- xml2::xml_text(formals_nodes)
          sig <- paste0("function(", paste(params, collapse = ", "), ")")
          if (nchar(sig) > 200) {
            sig <- paste0(substr(sig, 1, 200), "...")
          }

          roxy <- extract_roxygen(source_lines, func_line)

          functions[[length(functions) + 1]] <- list(
            name = func_name,
            signature = paste0(func_name, " <- ", sig),
            doc = roxy$doc,
            line = func_line,
            exported = roxy$exported || !grepl("^\\.", func_name)
          )
        }
      }

      # Check for R6Class.
      rhs_text <- xml2::xml_text(rhs)
      if (grepl("R6(::)?R6Class", rhs_text)) {
        lhs <- children[[1]]
        lhs_children <- xml2::xml_children(lhs)
        if (length(lhs_children) >= 1 && xml2::xml_name(lhs_children[[1]]) == "SYMBOL") {
          class_name <- xml2::xml_text(lhs_children[[1]])
          class_line <- as.integer(xml2::xml_attr(expr, "line1"))
          roxy <- extract_roxygen(source_lines, class_line)

          types[[length(types) + 1]] <- list(
            name = class_name,
            kind = "R6 class",
            doc = roxy$doc,
            line = class_line
          )
        }
      }
    }

    # Check for setClass("ClassName", ...) calls.
    if (length(child_names) >= 1 && child_names[1] == "expr") {
      first_expr <- children[[1]]
      first_children <- xml2::xml_children(first_expr)
      if (length(first_children) >= 1) {
        first_text <- xml2::xml_text(first_children[[1]])
        if (identical(first_text, "setClass") && "OP-LEFT-PAREN" %in% child_names) {
          # Find the first STR_CONST (class name).
          str_nodes <- xml2::xml_find_all(expr, ".//STR_CONST")
          if (length(str_nodes) >= 1) {
            class_name <- gsub("^['\"]|['\"]$", "", xml2::xml_text(str_nodes[[1]]))
            class_line <- as.integer(xml2::xml_attr(expr, "line1"))
            roxy <- extract_roxygen(source_lines, class_line)

            types[[length(types) + 1]] <- list(
              name = class_name,
              kind = "S4 class",
              doc = roxy$doc,
              line = class_line
            )
          }
        }
      }
    }
  }
}

# Build JSON output manually to avoid requiring jsonlite.
build_json <- function() {
  parts <- character(0)

  # Functions array.
  func_items <- character(0)
  for (f in functions) {
    item <- sprintf(
      '{"name":"%s","signature":"%s","doc":"%s","line":%d,"exported":%s}',
      json_escape(f$name),
      json_escape(f$signature),
      json_escape(f$doc),
      f$line,
      if (isTRUE(f$exported)) "true" else "false"
    )
    func_items <- c(func_items, item)
  }

  # Types array.
  type_items <- character(0)
  for (tp in types) {
    item <- sprintf(
      '{"name":"%s","kind":"%s","doc":"%s","line":%d}',
      json_escape(tp$name),
      json_escape(tp$kind),
      json_escape(tp$doc),
      tp$line
    )
    type_items <- c(type_items, item)
  }

  cat(sprintf(
    '{"functions":[%s],"types":[%s]}\n',
    paste(func_items, collapse = ","),
    paste(type_items, collapse = ",")
  ))
}

build_json()
