package autosignin

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Actual Python3.12 original-source observations, source-more.json SHA92c19d851bdb7016921d157a15a276b0a095f6feb7857380d322c5161eb2ed2c.
const sourceEdgeFixture = `{
  "python": "3.12.13",
  "classification": "actual original clean_history AST and stdlib json.loads/repr source runtime, synthetic only",
  "strptime_directives": {
    "Y": "(?P<Y>\\d\\d\\d\\d)",
    "m": "(?P<m>1[0-2]|0[1-9]|[1-9])",
    "d": "(?P<d>3[0-1]|[1-2]\\d|0[1-9]|[1-9]| [1-9])"
  },
  "json": [
    {
      "text": "",
      "value": null,
      "error": "Expecting value: line 1 column 1 (char 0)"
    },
    {
      "text": " ",
      "value": null,
      "error": "Expecting value: line 1 column 2 (char 1)"
    },
    {
      "text": "\ufeff{}",
      "value": null,
      "error": "Unexpected UTF-8 BOM (decode using utf-8-sig): line 1 column 1 (char 0)"
    },
    {
      "text": " \ufeff{}",
      "value": null,
      "error": "Expecting value: line 1 column 2 (char 1)"
    },
    {
      "text": "null",
      "value": {
        "type": "NoneType"
      },
      "error": null
    },
    {
      "text": "true",
      "value": {
        "type": "bool",
        "value": true
      },
      "error": null
    },
    {
      "text": "false",
      "value": {
        "type": "bool",
        "value": false
      },
      "error": null
    },
    {
      "text": "NaN",
      "value": {
        "type": "float",
        "value": "nan"
      },
      "error": null
    },
    {
      "text": "Infinity",
      "value": {
        "type": "float",
        "value": "inf"
      },
      "error": null
    },
    {
      "text": "-Infinity",
      "value": {
        "type": "float",
        "value": "-inf"
      },
      "error": null
    },
    {
      "text": "-NaN",
      "value": null,
      "error": "Expecting value: line 1 column 1 (char 0)"
    },
    {
      "text": "+Infinity",
      "value": null,
      "error": "Expecting value: line 1 column 1 (char 0)"
    },
    {
      "text": "nan",
      "value": null,
      "error": "Expecting value: line 1 column 1 (char 0)"
    },
    {
      "text": "0",
      "value": {
        "type": "int",
        "value": "0"
      },
      "error": null
    },
    {
      "text": "-0",
      "value": {
        "type": "int",
        "value": "0"
      },
      "error": null
    },
    {
      "text": "12",
      "value": {
        "type": "int",
        "value": "12"
      },
      "error": null
    },
    {
      "text": "01",
      "value": null,
      "error": "Extra data: line 1 column 2 (char 1)"
    },
    {
      "text": "-00",
      "value": null,
      "error": "Extra data: line 1 column 3 (char 2)"
    },
    {
      "text": "1.",
      "value": null,
      "error": "Extra data: line 1 column 2 (char 1)"
    },
    {
      "text": "1.e2",
      "value": null,
      "error": "Extra data: line 1 column 2 (char 1)"
    },
    {
      "text": "1e",
      "value": null,
      "error": "Extra data: line 1 column 2 (char 1)"
    },
    {
      "text": "1e+",
      "value": null,
      "error": "Extra data: line 1 column 2 (char 1)"
    },
    {
      "text": "1e-2",
      "value": {
        "type": "float",
        "value": "3f847ae147ae147b"
      },
      "error": null
    },
    {
      "text": "-",
      "value": null,
      "error": "Expecting value: line 1 column 1 (char 0)"
    },
    {
      "text": "+1",
      "value": null,
      "error": "Expecting value: line 1 column 1 (char 0)"
    },
    {
      "text": "-.5",
      "value": null,
      "error": "Expecting value: line 1 column 1 (char 0)"
    },
    {
      "text": ".5",
      "value": null,
      "error": "Expecting value: line 1 column 1 (char 0)"
    },
    {
      "text": "1e400",
      "value": {
        "type": "float",
        "value": "inf"
      },
      "error": null
    },
    {
      "text": "1_0",
      "value": null,
      "error": "Extra data: line 1 column 2 (char 1)"
    },
    {
      "text": "\u0661",
      "value": null,
      "error": "Expecting value: line 1 column 1 (char 0)"
    },
    {
      "text": "1\u0662",
      "value": null,
      "error": "Extra data: line 1 column 2 (char 1)"
    },
    {
      "text": "0.5",
      "value": {
        "type": "float",
        "value": "3fe0000000000000"
      },
      "error": null
    },
    {
      "text": "-0.0",
      "value": {
        "type": "float",
        "value": "8000000000000000"
      },
      "error": null
    },
    {
      "text": "1E01",
      "value": {
        "type": "float",
        "value": "4024000000000000"
      },
      "error": null
    },
    {
      "text": "1e-400",
      "value": {
        "type": "float",
        "value": "0000000000000000"
      },
      "error": null
    },
    {
      "text": "[]",
      "value": {
        "type": "list",
        "value": []
      },
      "error": null
    },
    {
      "text": "[ ]",
      "value": {
        "type": "list",
        "value": []
      },
      "error": null
    },
    {
      "text": "[1,]",
      "value": null,
      "error": "Expecting value: line 1 column 4 (char 3)"
    },
    {
      "text": "[1",
      "value": null,
      "error": "Expecting ',' delimiter: line 1 column 3 (char 2)"
    },
    {
      "text": "[1 2]",
      "value": null,
      "error": "Expecting ',' delimiter: line 1 column 4 (char 3)"
    },
    {
      "text": "[",
      "value": null,
      "error": "Expecting value: line 1 column 2 (char 1)"
    },
    {
      "text": "[true,false,null,NaN,Infinity]",
      "value": {
        "type": "list",
        "value": [
          {
            "type": "bool",
            "value": true
          },
          {
            "type": "bool",
            "value": false
          },
          {
            "type": "NoneType"
          },
          {
            "type": "float",
            "value": "nan"
          },
          {
            "type": "float",
            "value": "inf"
          }
        ]
      },
      "error": null
    },
    {
      "text": "{}",
      "value": {
        "type": "dict",
        "value": {}
      },
      "error": null
    },
    {
      "text": "{\"a\":1}",
      "value": {
        "type": "dict",
        "value": {
          "a": {
            "type": "int",
            "value": "1"
          }
        }
      },
      "error": null
    },
    {
      "text": "{a:1}",
      "value": null,
      "error": "Expecting property name enclosed in double quotes: line 1 column 2 (char 1)"
    },
    {
      "text": "{\"a\"1}",
      "value": null,
      "error": "Expecting ':' delimiter: line 1 column 5 (char 4)"
    },
    {
      "text": "{\"a\":}",
      "value": null,
      "error": "Expecting value: line 1 column 6 (char 5)"
    },
    {
      "text": "{\"a\":1,}",
      "value": null,
      "error": "Expecting property name enclosed in double quotes: line 1 column 8 (char 7)"
    },
    {
      "text": "{\"a\":1",
      "value": null,
      "error": "Expecting ',' delimiter: line 1 column 7 (char 6)"
    },
    {
      "text": "{\"a\":1 \"b\":2}",
      "value": null,
      "error": "Expecting ',' delimiter: line 1 column 8 (char 7)"
    },
    {
      "text": "{\"a\":1,\"a\":2}",
      "value": {
        "type": "dict",
        "value": {
          "a": {
            "type": "int",
            "value": "2"
          }
        }
      },
      "error": null
    },
    {
      "text": "{\"\u534e\u8bed\":[null,NaN,2.0]}",
      "value": {
        "type": "dict",
        "value": {
          "\u534e\u8bed": {
            "type": "list",
            "value": [
              {
                "type": "NoneType"
              },
              {
                "type": "float",
                "value": "nan"
              },
              {
                "type": "float",
                "value": "4000000000000000"
              }
            ]
          }
        }
      },
      "error": null
    },
    {
      "text": "[\n{\"x\":\"\ud83d\ude42\"},\n]",
      "value": null,
      "error": "Expecting value: line 3 column 1 (char 13)"
    },
    {
      "text": "\"\"",
      "value": {
        "type": "str",
        "value": []
      },
      "error": null
    },
    {
      "text": "\"abc\"",
      "value": {
        "type": "str",
        "value": [
          97,
          98,
          99
        ]
      },
      "error": null
    },
    {
      "text": "\"abc",
      "value": null,
      "error": "Unterminated string starting at: line 1 column 1 (char 0)"
    },
    {
      "text": "\"abc\\",
      "value": null,
      "error": "Unterminated string starting at: line 1 column 1 (char 0)"
    },
    {
      "text": "\"\\q\"",
      "value": null,
      "error": "Invalid \\escape: line 1 column 2 (char 1)"
    },
    {
      "text": "\"\\u0000\"",
      "value": {
        "type": "str",
        "value": [
          0
        ]
      },
      "error": null
    },
    {
      "text": "\"\\u123\"",
      "value": null,
      "error": "Invalid \\uXXXX escape: line 1 column 3 (char 2)"
    },
    {
      "text": "\"\\uZZZZ\"",
      "value": null,
      "error": "Invalid \\uXXXX escape: line 1 column 3 (char 2)"
    },
    {
      "text": "\"\\x00\"",
      "value": null,
      "error": "Invalid \\escape: line 1 column 2 (char 1)"
    },
    {
      "text": "\"a\tb\"",
      "value": null,
      "error": "Invalid control character at: line 1 column 3 (char 2)"
    },
    {
      "text": "\"a\nb\"",
      "value": null,
      "error": "Invalid control character at: line 1 column 3 (char 2)"
    },
    {
      "text": "\"\\b\\f\\n\\r\\t\\/\\\\\\\"\"",
      "value": {
        "type": "str",
        "value": [
          8,
          12,
          10,
          13,
          9,
          47,
          92,
          34
        ]
      },
      "error": null
    },
    {
      "text": "\"\\ud83d\\ude00\"",
      "value": {
        "type": "str",
        "value": [
          128512
        ]
      },
      "error": null
    },
    {
      "text": "true false",
      "value": null,
      "error": "Extra data: line 1 column 6 (char 5)"
    },
    {
      "text": "{}[]",
      "value": null,
      "error": "Extra data: line 1 column 3 (char 2)"
    },
    {
      "text": "[1.]",
      "value": null,
      "error": "Expecting ',' delimiter: line 1 column 3 (char 2)"
    },
    {
      "text": "[1e]",
      "value": null,
      "error": "Expecting ',' delimiter: line 1 column 3 (char 2)"
    },
    {
      "text": "{\"a\":1.}",
      "value": null,
      "error": "Expecting ',' delimiter: line 1 column 7 (char 6)"
    }
  ],
  "dates": [
    {
      "key": "\u7b7e\u5230-2024-02-29",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-2024-02-\u0662\u0669",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-02-2\u0669",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-2024-02-3\u0660",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-02-31",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-02-0\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-02-01",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-2024-02-\u0660\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-02-1\uff12",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-2024-02-\uff122",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-\u0660\u0662-29",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-\u0660\u0662-\u0662\u0669",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-\u0660\u0662-2\u0669",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-\u0660\u0662-3\u0660",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-\u0660\u0662-31",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-\u0660\u0662-0\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-\u0660\u0662-01",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-\u0660\u0662-\u0660\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-\u0660\u0662-1\uff12",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-\u0660\u0662-\uff122",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-2-29",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-2-\u0662\u0669",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-2-2\u0669",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-2-3\u0660",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-2-31",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-2-0\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-2-01",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-2-\u0660\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-2-1\uff12",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-2-\uff122",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-0\uff12-29",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-0\uff12-\u0662\u0669",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-0\uff12-2\u0669",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-0\uff12-3\u0660",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-0\uff12-31",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-0\uff12-0\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-0\uff12-01",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-0\uff12-\u0660\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-0\uff12-1\uff12",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-0\uff12-\uff122",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-12-29",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-2024-12-\u0662\u0669",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-12-2\u0669",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-2024-12-3\u0660",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-12-31",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-2024-12-0\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-12-01",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-2024-12-\u0660\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-2024-12-1\uff12",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-2024-12-\uff122",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-02-29",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-02-\u0662\u0669",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-02-2\u0669",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-02-3\u0660",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-02-31",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-02-0\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-02-01",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-02-\u0660\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-02-1\uff12",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-02-\uff122",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-\u0660\u0662-29",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-\u0660\u0662-\u0662\u0669",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-\u0660\u0662-2\u0669",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-\u0660\u0662-3\u0660",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-\u0660\u0662-31",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-\u0660\u0662-0\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-\u0660\u0662-01",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-\u0660\u0662-\u0660\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-\u0660\u0662-1\uff12",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-\u0660\u0662-\uff122",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-2-29",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-2-\u0662\u0669",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-2-2\u0669",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-2-3\u0660",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-2-31",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-2-0\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-2-01",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-2-\u0660\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-2-1\uff12",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-2-\uff122",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-0\uff12-29",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-0\uff12-\u0662\u0669",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-0\uff12-2\u0669",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-0\uff12-3\u0660",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-0\uff12-31",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-0\uff12-0\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-0\uff12-01",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-0\uff12-\u0660\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-0\uff12-1\uff12",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-0\uff12-\uff122",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-12-29",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-12-\u0662\u0669",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-12-2\u0669",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-12-3\u0660",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-12-31",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-12-0\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-12-01",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-12-\u0660\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-12-1\uff12",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-\u0662\u0660\u0662\u0664-12-\uff122",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-02-29",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-02-\u0662\u0669",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-02-2\u0669",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-02-3\u0660",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-02-31",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-02-0\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-02-01",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-02-\u0660\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-02-1\uff12",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-02-\uff122",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-\u0660\u0662-29",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-\u0660\u0662-\u0662\u0669",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-\u0660\u0662-2\u0669",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-\u0660\u0662-3\u0660",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-\u0660\u0662-31",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-\u0660\u0662-0\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-\u0660\u0662-01",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-\u0660\u0662-\u0660\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-\u0660\u0662-1\uff12",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-\u0660\u0662-\uff122",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-2-29",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-2-\u0662\u0669",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-2-2\u0669",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-2-3\u0660",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-2-31",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-2-0\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-2-01",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-2-\u0660\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-2-1\uff12",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-2-\uff122",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-0\uff12-29",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-0\uff12-\u0662\u0669",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-0\uff12-2\u0669",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-0\uff12-3\u0660",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-0\uff12-31",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-0\uff12-0\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-0\uff12-01",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-0\uff12-\u0660\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-0\uff12-1\uff12",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-0\uff12-\uff122",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-12-29",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-12-\u0662\u0669",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-12-2\u0669",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-12-3\u0660",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-12-31",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-12-0\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-12-01",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-12-\u0660\u0661",
      "deleted": false
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-12-1\uff12",
      "deleted": true
    },
    {
      "key": "\u7b7e\u5230-\uff12\uff10\uff12\uff14-12-\uff122",
      "deleted": false
    }
  ],
  "repr": [
    {
      "text": "x\u0000y",
      "repr": "'x\\x00y'"
    },
    {
      "text": "x\u0001y",
      "repr": "'x\\x01y'"
    },
    {
      "text": "x\u0002y",
      "repr": "'x\\x02y'"
    },
    {
      "text": "x\u0003y",
      "repr": "'x\\x03y'"
    },
    {
      "text": "x\u0004y",
      "repr": "'x\\x04y'"
    },
    {
      "text": "x\u0005y",
      "repr": "'x\\x05y'"
    },
    {
      "text": "x\u0006y",
      "repr": "'x\\x06y'"
    },
    {
      "text": "x\u0007y",
      "repr": "'x\\x07y'"
    },
    {
      "text": "x\by",
      "repr": "'x\\x08y'"
    },
    {
      "text": "x\ty",
      "repr": "'x\\ty'"
    },
    {
      "text": "x\ny",
      "repr": "'x\\ny'"
    },
    {
      "text": "x\u000by",
      "repr": "'x\\x0by'"
    },
    {
      "text": "x\fy",
      "repr": "'x\\x0cy'"
    },
    {
      "text": "x\ry",
      "repr": "'x\\ry'"
    },
    {
      "text": "x\u000ey",
      "repr": "'x\\x0ey'"
    },
    {
      "text": "x\u000fy",
      "repr": "'x\\x0fy'"
    },
    {
      "text": "x\u0010y",
      "repr": "'x\\x10y'"
    },
    {
      "text": "x\u0011y",
      "repr": "'x\\x11y'"
    },
    {
      "text": "x\u0012y",
      "repr": "'x\\x12y'"
    },
    {
      "text": "x\u0013y",
      "repr": "'x\\x13y'"
    },
    {
      "text": "x\u0014y",
      "repr": "'x\\x14y'"
    },
    {
      "text": "x\u0015y",
      "repr": "'x\\x15y'"
    },
    {
      "text": "x\u0016y",
      "repr": "'x\\x16y'"
    },
    {
      "text": "x\u0017y",
      "repr": "'x\\x17y'"
    },
    {
      "text": "x\u0018y",
      "repr": "'x\\x18y'"
    },
    {
      "text": "x\u0019y",
      "repr": "'x\\x19y'"
    },
    {
      "text": "x\u001ay",
      "repr": "'x\\x1ay'"
    },
    {
      "text": "x\u001by",
      "repr": "'x\\x1by'"
    },
    {
      "text": "x\u001cy",
      "repr": "'x\\x1cy'"
    },
    {
      "text": "x\u001dy",
      "repr": "'x\\x1dy'"
    },
    {
      "text": "x\u001ey",
      "repr": "'x\\x1ey'"
    },
    {
      "text": "x\u001fy",
      "repr": "'x\\x1fy'"
    },
    {
      "text": "x y",
      "repr": "'x y'"
    },
    {
      "text": "x\u007fy",
      "repr": "'x\\x7fy'"
    },
    {
      "text": "x\u0080y",
      "repr": "'x\\x80y'"
    },
    {
      "text": "x\u0081y",
      "repr": "'x\\x81y'"
    },
    {
      "text": "x\u0082y",
      "repr": "'x\\x82y'"
    },
    {
      "text": "x\u0083y",
      "repr": "'x\\x83y'"
    },
    {
      "text": "x\u0084y",
      "repr": "'x\\x84y'"
    },
    {
      "text": "x\u0085y",
      "repr": "'x\\x85y'"
    },
    {
      "text": "x\u0086y",
      "repr": "'x\\x86y'"
    },
    {
      "text": "x\u0087y",
      "repr": "'x\\x87y'"
    },
    {
      "text": "x\u0088y",
      "repr": "'x\\x88y'"
    },
    {
      "text": "x\u0089y",
      "repr": "'x\\x89y'"
    },
    {
      "text": "x\u008ay",
      "repr": "'x\\x8ay'"
    },
    {
      "text": "x\u008by",
      "repr": "'x\\x8by'"
    },
    {
      "text": "x\u008cy",
      "repr": "'x\\x8cy'"
    },
    {
      "text": "x\u008dy",
      "repr": "'x\\x8dy'"
    },
    {
      "text": "x\u008ey",
      "repr": "'x\\x8ey'"
    },
    {
      "text": "x\u008fy",
      "repr": "'x\\x8fy'"
    },
    {
      "text": "x\u0090y",
      "repr": "'x\\x90y'"
    },
    {
      "text": "x\u0091y",
      "repr": "'x\\x91y'"
    },
    {
      "text": "x\u0092y",
      "repr": "'x\\x92y'"
    },
    {
      "text": "x\u0093y",
      "repr": "'x\\x93y'"
    },
    {
      "text": "x\u0094y",
      "repr": "'x\\x94y'"
    },
    {
      "text": "x\u0095y",
      "repr": "'x\\x95y'"
    },
    {
      "text": "x\u0096y",
      "repr": "'x\\x96y'"
    },
    {
      "text": "x\u0097y",
      "repr": "'x\\x97y'"
    },
    {
      "text": "x\u0098y",
      "repr": "'x\\x98y'"
    },
    {
      "text": "x\u0099y",
      "repr": "'x\\x99y'"
    },
    {
      "text": "x\u009ay",
      "repr": "'x\\x9ay'"
    },
    {
      "text": "x\u009by",
      "repr": "'x\\x9by'"
    },
    {
      "text": "x\u009cy",
      "repr": "'x\\x9cy'"
    },
    {
      "text": "x\u009dy",
      "repr": "'x\\x9dy'"
    },
    {
      "text": "x\u009ey",
      "repr": "'x\\x9ey'"
    },
    {
      "text": "x\u009fy",
      "repr": "'x\\x9fy'"
    },
    {
      "text": "x\u00a0y",
      "repr": "'x\\xa0y'"
    },
    {
      "text": "x\u1680y",
      "repr": "'x\\u1680y'"
    },
    {
      "text": "x\u2000y",
      "repr": "'x\\u2000y'"
    },
    {
      "text": "x\u2007y",
      "repr": "'x\\u2007y'"
    },
    {
      "text": "x\u2028y",
      "repr": "'x\\u2028y'"
    },
    {
      "text": "x\u202fy",
      "repr": "'x\\u202fy'"
    },
    {
      "text": "x\u3000y",
      "repr": "'x\\u3000y'"
    },
    {
      "text": "x\u0378y",
      "repr": "'x\\u0378y'"
    },
    {
      "text": "x\udbff\udfffy",
      "repr": "'x\\U0010ffffy'"
    },
    {
      "text": "x\ud83d\ude00y",
      "repr": "'x\ud83d\ude00y'"
    },
    {
      "text": "a'b",
      "repr": "\"a'b\""
    },
    {
      "text": "a\"b",
      "repr": "'a\"b'"
    },
    {
      "text": "a'\"b",
      "repr": "'a\\'\"b'"
    },
    {
      "text": "x\\y",
      "repr": "'x\\\\y'"
    },
    {
      "text": "\u0000\n\r\t\u0007\u00a0",
      "repr": "'\\x00\\n\\r\\t\\x07\\xa0'"
    },
    {
      "text": "\u6c49\u5b57\ud83d\ude42",
      "repr": "'\u6c49\u5b57\ud83d\ude42'"
    }
  ],
  "source_sha256": "1d8329afc00bc218710e302e0eda915351731d41101df08fcf378e90440b9220"
}
`

func sourceEdgeTyped(value any) any {
	switch x := value.(type) {
	case sourceHTTPMap:
		return sourceEdgeTyped(x.values)
	case nil:
		return Object{"type": "NoneType"}
	case bool:
		return Object{"type": "bool", "value": x}
	case string:
		points := []int{}
		for _, r := range sourceCodepoints(x) {
			points = append(points, int(r))
		}
		return Object{"type": "str", "value": points}
	case json.Number:
		if !strings.ContainsAny(string(x), ".eE") {
			return Object{"type": "int", "value": string(x)}
		}
		f, _ := strconv.ParseFloat(string(x), 64)
		return sourceEdgeTyped(f)
	case float64:
		text := fmt.Sprintf("%016x", math.Float64bits(x))
		if math.IsNaN(x) {
			text = "nan"
		} else if math.IsInf(x, 1) {
			text = "inf"
		} else if math.IsInf(x, -1) {
			text = "-inf"
		}
		return Object{"type": "float", "value": text}
	case []any:
		rows := []any{}
		for _, v := range x {
			rows = append(rows, sourceEdgeTyped(v))
		}
		return Object{"type": "list", "value": rows}
	case Object:
		rows := Object{}
		for k, v := range x {
			rows[k] = sourceEdgeTyped(v)
		}
		return Object{"type": "dict", "value": rows}
	}
	return fmt.Sprintf("unexpected %T", value)
}
func TestAuthorSourceEdgeRules(t *testing.T) {
	raw := []byte(sourceEdgeFixture)
	var err error
	var fixture struct {
		JSON []struct {
			Text  string
			Value any
			Error *string
		}
		Dates []struct {
			Key     string
			Deleted bool
		}
		Repr []struct{ Text, Repr string }
	}
	if err = Decode(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	t.Run("HTTPJSON", func(t *testing.T) {
		for i, c := range fixture.JSON {
			t.Run(strconv.Itoa(i), func(t *testing.T) {
				value, err := parseSiteJSON(c.Text)
				if c.Error != nil {
					if err == nil || err.Error() != *c.Error {
						t.Fatalf("text=%q got=%v source=%q", c.Text, err, *c.Error)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				a, _ := json.Marshal(sourceEdgeTyped(value))
				b, _ := json.Marshal(c.Value)
				if string(a) != string(b) {
					t.Fatalf("text=%q got=%s source=%s", c.Text, a, b)
				}
			})
		}
	})
	t.Run("HistoryDates", func(t *testing.T) {
		now := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
		for i, c := range fixture.Dates {
			t.Run(strconv.Itoa(i), func(t *testing.T) {
				e := New(Inventory{}, func() time.Time { return now })
				e.Data[c.Key] = true
				if err := e.CleanHistory(now, []string{c.Key}); err != nil {
					t.Fatal(err)
				}
				_, exists := e.Data[c.Key]
				if !exists != c.Deleted {
					t.Fatalf("key=%q deleted=%v source=%v", c.Key, !exists, c.Deleted)
				}
			})
		}
	})
	t.Run("PythonRepr", func(t *testing.T) {
		for i, c := range fixture.Repr {
			t.Run(strconv.Itoa(i), func(t *testing.T) {
				if got := sourceRepr(c.Text); got != c.Repr {
					t.Fatalf("text=%q got=%q source=%q", c.Text, got, c.Repr)
				}
			})
		}
	})
}
func TestAuthorHTTPJSONDoesNotRelaxIPC(t *testing.T) {
	for _, text := range []string{`{"code":0,"unused":NaN}`, `{"code":0,"unused":Infinity}`, `{"code":0,"unused":-Infinity}`} {
		if _, err := parseSiteJSON(text); err != nil {
			t.Fatal(err)
		}
		var value any
		if Decode([]byte(text), &value) == nil {
			t.Fatal("nonfinite HTTP extension accepted as IPC JSON")
		}
	}
}

// Actual Python3.12 JSON nonfinite -> int source observation SHAe774657eddadcf3467875dda07eb193a421cbf2811f053a900189f88e9be288d.
func TestAuthorHTTPNonfiniteIntegerFailure(t *testing.T) {
	const fixture = `{
  "python": "3.12.13",
  "classification": "actual Python3.12 JSON float then int, matching ZhuQue bonus conversion operator; no app/network",
  "cases": [
    {
      "text": "NaN",
      "kind": "ValueError",
      "error": "cannot convert float NaN to integer"
    },
    {
      "text": "Infinity",
      "kind": "OverflowError",
      "error": "cannot convert float infinity to integer"
    },
    {
      "text": "-Infinity",
      "kind": "OverflowError",
      "error": "cannot convert float infinity to integer"
    }
  ]
}
`
	var source struct {
		Cases []struct{ Text, Kind, Error string }
	}
	if err := Decode([]byte(fixture), &source); err != nil {
		t.Fatal(err)
	}
	for _, c := range source.Cases {
		t.Run(c.Text, func(t *testing.T) {
			v, err := parseSiteJSON(c.Text)
			if err != nil {
				t.Fatal(err)
			}
			_, err = sourceInteger(v)
			var failure *ValueError
			if !errors.As(err, &failure) || failure.Kind != c.Kind || failure.Error() != c.Error {
				t.Fatalf("value=%s error=%v source=%s:%s", c.Text, err, c.Kind, c.Error)
			}
		})
	}
}
