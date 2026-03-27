"""
A simple data pipeline framework for transforming records.
"""

from typing import Any, Callable, Iterator


class TransformError(Exception):
    """Raised when a pipeline transformation step fails."""

    def __init__(self, step_name: str, record: Any, cause: Exception):
        self.step_name = step_name
        self.record = record
        self.cause = cause
        super().__init__(f"Transform '{step_name}' failed: {cause}")


class Pipeline:
    """
    A composable data pipeline that applies a sequence of transformations
    to a stream of records.

    Example:
        pipeline = Pipeline("my_pipeline")
        pipeline.add_step("normalize", normalize_record)
        pipeline.add_step("validate", validate_record)
        results = list(pipeline.run(raw_records))
    """

    def __init__(self, name: str):
        self.name = name
        self._steps: list[tuple[str, Callable]] = []

    def add_step(self, name: str, transform: Callable) -> "Pipeline":
        """Add a named transformation step to the pipeline."""
        self._steps.append((name, transform))
        return self

    def run(self, records: Iterator[Any]) -> Iterator[Any]:
        """
        Run all records through the pipeline steps in order.
        Yields transformed records. Raises TransformError on failure.
        """
        for record in records:
            result = record
            for step_name, transform in self._steps:
                try:
                    result = transform(result)
                except Exception as e:
                    raise TransformError(step_name, record, e) from e
            yield result

    @property
    def step_count(self) -> int:
        """Returns the number of steps in the pipeline."""
        return len(self._steps)


def filter_step(predicate: Callable[[Any], bool]) -> Callable:
    """Creates a filter step that drops records not matching the predicate."""

    def _filter(record: Any) -> Any:
        if not predicate(record):
            return None
        return record

    return _filter


def map_field(field: str, transform: Callable) -> Callable:
    """Creates a step that transforms a single field of a dict record."""

    def _map(record: dict) -> dict:
        if field in record:
            record[field] = transform(record[field])
        return record

    return _map
