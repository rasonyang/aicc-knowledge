-- SPDX-License-Identifier: Apache-2.0
--
-- Q&A workbooks that already hold curated question and answer columns are
-- imported directly (a `<name>.qa.yaml` mapping next to the workbook). Parse
-- writes one parsed section per Q&A row, kind XLSX_QA_ROW, and generate turns
-- it into a candidate without asking the LLM to invent anything.
--
-- A Q&A row keeps its parts apart: body is the answer exactly as written (it
-- is also what the review workbook shows as the source excerpt), qa_question
-- is the question, qa_alternates the alternate questions read from the
-- mapped columns, and qa_language the language the mapping or the detection
-- settled on at parse time. The three qa_ columns are NULL, or empty for the
-- array, on every other kind of section.
--
-- The kind CHECK is replaced, not added to: the gate test in internal/store
-- requires exactly one CHECK per enum column and equality with the Go set.

-- +goose Up
ALTER TABLE parsed_sections DROP CONSTRAINT parsed_sections_kind_check;
ALTER TABLE parsed_sections ADD CONSTRAINT parsed_sections_kind_check
    CHECK (kind IN ('DOCX_SECTION', 'XLSX_CHUNK', 'XLSX_QA_ROW'));
ALTER TABLE parsed_sections
    ADD COLUMN qa_question   text,
    ADD COLUMN qa_alternates text[] NOT NULL DEFAULT '{}',
    ADD COLUMN qa_language   varchar(2) CHECK (qa_language IN ('EN', 'ZH')),
    ADD CONSTRAINT ck_parsed_sections_qa_row_has_question CHECK ((kind = 'XLSX_QA_ROW') = (qa_question IS NOT NULL));

-- +goose Down
DELETE FROM parsed_sections WHERE kind = 'XLSX_QA_ROW';
ALTER TABLE parsed_sections
    DROP CONSTRAINT ck_parsed_sections_qa_row_has_question,
    DROP COLUMN qa_language,
    DROP COLUMN qa_alternates,
    DROP COLUMN qa_question;
ALTER TABLE parsed_sections DROP CONSTRAINT parsed_sections_kind_check;
ALTER TABLE parsed_sections ADD CONSTRAINT parsed_sections_kind_check
    CHECK (kind IN ('DOCX_SECTION', 'XLSX_CHUNK'));
