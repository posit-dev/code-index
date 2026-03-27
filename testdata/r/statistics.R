#' Weighted Mean
#'
#' Computes the weighted arithmetic mean of a numeric vector.
#'
#' @param x Numeric vector of values.
#' @param weights Numeric vector of weights (same length as x).
#' @return The weighted mean as a single numeric value.
#' @export
weighted_mean <- function(x, weights) {
  if (length(x) != length(weights)) {
    stop("x and weights must have the same length")
  }
  sum(x * weights) / sum(weights)
}

#' Moving Average
#'
#' Computes a simple moving average over a window of size k.
#'
#' @param x Numeric vector of values.
#' @param k Window size (integer).
#' @return Numeric vector of moving averages (length = length(x) - k + 1).
#' @export
moving_average <- function(x, k = 3) {
  n <- length(x)
  if (k > n) stop("Window size k cannot exceed length of x")
  result <- numeric(n - k + 1)
  for (i in seq_along(result)) {
    result[i] <- mean(x[i:(i + k - 1)])
  }
  result
}

#' Standard Error of the Mean
#'
#' Computes the standard error of the mean for a numeric vector.
#'
#' @param x Numeric vector of values.
#' @return The standard error as a single numeric value.
#' @export
se_mean <- function(x) {
  sd(x) / sqrt(length(x))
}

#' Normalize Vector
#'
#' Scales a numeric vector to have mean 0 and standard deviation 1.
#'
#' @param x Numeric vector to normalize.
#' @return Normalized numeric vector.
normalize <- function(x) {
  (x - mean(x)) / sd(x)
}
