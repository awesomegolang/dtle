package sql

import (
	"fmt"
	"strconv"
	"strings"

	"bytes"
	umconf "udup/internal/config/mysql"
)

type ValueComparisonSign string

const (
	LessThanComparisonSign            ValueComparisonSign = "<"
	LessThanOrEqualsComparisonSign                        = "<="
	EqualsComparisonSign                                  = "="
	GreaterThanOrEqualsComparisonSign                     = ">="
	GreaterThanComparisonSign                             = ">"
	NotEqualsComparisonSign                               = "!="
)

// EscapeName will escape a db/table/column/... name by wrapping with backticks.
// It is not fool proof. I'm just trying to do the right thing here, not solving
// SQL injection issues, which should be irrelevant for this tool.
func EscapeName(name string) string {
	if unquoted, err := strconv.Unquote(name); err == nil {
		name = unquoted
	}
	return fmt.Sprintf("`%s`", name)
}

func buildColumnsPreparedValues(columns *umconf.ColumnList) []string {
	values := make([]string, columns.Len(), columns.Len())
	for i, column := range columns.ColumnList() {
		var token string
		if column.TimezoneConversion != nil {
			token = fmt.Sprintf("convert_tz(?, '%s', '%s')", column.TimezoneConversion.ToTimezone, "+00:00")
		} else {
			token = "?"
		}
		values[i] = token
	}
	return values
}

func buildPreparedValues(length int) []string {
	values := make([]string, length, length)
	for i := 0; i < length; i++ {
		values[i] = "?"
	}
	return values
}

func duplicateNames(names []string) []string {
	duplicate := make([]string, len(names), len(names))
	copy(duplicate, names)
	return duplicate
}

func BuildValueComparison(column string, value string, comparisonSign ValueComparisonSign) (result string, err error) {
	if column == "" {
		return "", fmt.Errorf("Empty column in GetValueComparison")
	}
	if value == "" {
		return "", fmt.Errorf("Empty value in GetValueComparison")
	}
	comparison := fmt.Sprintf("(%s %s %s)", EscapeName(column), string(comparisonSign), value)
	return comparison, err
}

func BuildEqualsComparison(columns []string, values []string) (result string, err error) {
	if len(columns) == 0 {
		return "", fmt.Errorf("Got 0 columns in GetEqualsComparison")
	}
	if len(columns) != len(values) {
		return "", fmt.Errorf("Got %d columns but %d values in GetEqualsComparison", len(columns), len(values))
	}
	comparisons := []string{}
	for i, column := range columns {
		value := values[i]
		comparison, err := BuildValueComparison(column, value, EqualsComparisonSign)
		if err != nil {
			return "", err
		}
		comparisons = append(comparisons, comparison)
	}
	result = strings.Join(comparisons, " and ")
	result = fmt.Sprintf("(%s)", result)
	return result, nil
}

func BuildEqualsPreparedComparison(columns []string) (result string, err error) {
	values := buildPreparedValues(len(columns))
	return BuildEqualsComparison(columns, values)
}

func BuildSetPreparedClause(columns *umconf.ColumnList) (result string, err error) {
	if columns.Len() == 0 {
		return "", fmt.Errorf("Got 0 columns in BuildSetPreparedClause")
	}
	setTokens := []string{}
	for _, column := range columns.ColumnList() {
		var setToken string
		if column.TimezoneConversion != nil {
			setToken = fmt.Sprintf("%s=convert_tz(?, '%s', '%s')", EscapeName(column.Name), column.TimezoneConversion.ToTimezone, "+00:00")
		} else {
			setToken = fmt.Sprintf("%s=?", EscapeName(column.Name))
		}
		setTokens = append(setTokens, setToken)
	}
	return strings.Join(setTokens, ", "), nil
}

func BuildRangeComparison(columns []string, values []string, args []interface{}, comparisonSign ValueComparisonSign) (result string, explodedArgs []interface{}, err error) {
	if len(columns) == 0 {
		return "", explodedArgs, fmt.Errorf("Got 0 columns in GetRangeComparison")
	}
	if len(columns) != len(values) {
		return "", explodedArgs, fmt.Errorf("Got %d columns but %d values in GetEqualsComparison", len(columns), len(values))
	}
	if len(columns) != len(args) {
		return "", explodedArgs, fmt.Errorf("Got %d columns but %d args in GetEqualsComparison", len(columns), len(args))
	}
	includeEquals := false
	if comparisonSign == LessThanOrEqualsComparisonSign {
		comparisonSign = LessThanComparisonSign
		includeEquals = true
	}
	if comparisonSign == GreaterThanOrEqualsComparisonSign {
		comparisonSign = GreaterThanComparisonSign
		includeEquals = true
	}
	comparisons := []string{}

	for i, column := range columns {
		//
		value := values[i]
		rangeComparison, err := BuildValueComparison(column, value, comparisonSign)
		if err != nil {
			return "", explodedArgs, err
		}
		if len(columns[0:i]) > 0 {
			equalitiesComparison, err := BuildEqualsComparison(columns[0:i], values[0:i])
			if err != nil {
				return "", explodedArgs, err
			}
			comparison := fmt.Sprintf("(%s AND %s)", equalitiesComparison, rangeComparison)
			comparisons = append(comparisons, comparison)
			explodedArgs = append(explodedArgs, args[0:i]...)
			explodedArgs = append(explodedArgs, args[i])
		} else {
			comparisons = append(comparisons, rangeComparison)
			explodedArgs = append(explodedArgs, args[i])
		}
	}

	if includeEquals {
		comparison, err := BuildEqualsComparison(columns, values)
		if err != nil {
			return "", explodedArgs, nil
		}
		comparisons = append(comparisons, comparison)
		explodedArgs = append(explodedArgs, args...)
	}
	result = strings.Join(comparisons, " or ")
	result = fmt.Sprintf("(%s)", result)
	return result, explodedArgs, nil
}

func BuildRangePreparedComparison(columns *umconf.ColumnList, args []interface{}, comparisonSign ValueComparisonSign) (result string, explodedArgs []interface{}, err error) {
	values := buildColumnsPreparedValues(columns)
	return BuildRangeComparison(columns.Names(), values, args, comparisonSign)
}

func BuildRangeInsertQuery(databaseName, originalTableName, ghostTableName string, sharedColumns []string, mappedSharedColumns []string, uniqueKey string, uniqueKeyColumns *umconf.ColumnList, rangeStartValues, rangeEndValues []string, rangeStartArgs, rangeEndArgs []interface{}, includeRangeStartValues bool, transactionalTable bool) (result string, explodedArgs []interface{}, err error) {
	if len(sharedColumns) == 0 {
		return "", explodedArgs, fmt.Errorf("Got 0 shared columns in BuildRangeInsertQuery")
	}
	databaseName = EscapeName(databaseName)
	originalTableName = EscapeName(originalTableName)
	ghostTableName = EscapeName(ghostTableName)

	mappedSharedColumns = duplicateNames(mappedSharedColumns)
	for i := range mappedSharedColumns {
		mappedSharedColumns[i] = EscapeName(mappedSharedColumns[i])
	}
	mappedSharedColumnsListing := strings.Join(mappedSharedColumns, ", ")

	sharedColumns = duplicateNames(sharedColumns)
	for i := range sharedColumns {
		sharedColumns[i] = EscapeName(sharedColumns[i])
	}
	sharedColumnsListing := strings.Join(sharedColumns, ", ")

	uniqueKey = EscapeName(uniqueKey)
	var minRangeComparisonSign ValueComparisonSign = GreaterThanComparisonSign
	if includeRangeStartValues {
		minRangeComparisonSign = GreaterThanOrEqualsComparisonSign
	}
	rangeStartComparison, rangeExplodedArgs, err := BuildRangeComparison(uniqueKeyColumns.Names(), rangeStartValues, rangeStartArgs, minRangeComparisonSign)
	if err != nil {
		return "", explodedArgs, err
	}
	explodedArgs = append(explodedArgs, rangeExplodedArgs...)
	rangeEndComparison, rangeExplodedArgs, err := BuildRangeComparison(uniqueKeyColumns.Names(), rangeEndValues, rangeEndArgs, LessThanOrEqualsComparisonSign)
	if err != nil {
		return "", explodedArgs, err
	}
	explodedArgs = append(explodedArgs, rangeExplodedArgs...)
	transactionalClause := ""
	if transactionalTable {
		transactionalClause = "lock in share mode"
	}
	result = fmt.Sprintf(`
      insert ignore into %s.%s (%s)
      (select %s from %s.%s force index (%s)
        where (%s and %s) %s
      )
    `, databaseName, ghostTableName, mappedSharedColumnsListing,
		sharedColumnsListing, databaseName, originalTableName, uniqueKey,
		rangeStartComparison, rangeEndComparison, transactionalClause)
	return result, explodedArgs, nil
}

func BuildRangeInsertPreparedQuery(databaseName, originalTableName, ghostTableName string, sharedColumns []string, mappedSharedColumns []string, uniqueKey string, uniqueKeyColumns *umconf.ColumnList, rangeStartArgs, rangeEndArgs []interface{}, includeRangeStartValues bool, transactionalTable bool) (result string, explodedArgs []interface{}, err error) {
	rangeStartValues := buildColumnsPreparedValues(uniqueKeyColumns)
	rangeEndValues := buildColumnsPreparedValues(uniqueKeyColumns)
	return BuildRangeInsertQuery(databaseName, originalTableName, ghostTableName, sharedColumns, mappedSharedColumns, uniqueKey, uniqueKeyColumns, rangeStartValues, rangeEndValues, rangeStartArgs, rangeEndArgs, includeRangeStartValues, transactionalTable)
}

func BuildRangeSelectQuery(databaseName, originalTableName, ghostTableName string, sharedColumns []string, mappedSharedColumns []string, uniqueKey string, uniqueKeyColumns *umconf.ColumnList, rangeStartValues, rangeEndValues []string, rangeStartArgs, rangeEndArgs []interface{}, includeRangeStartValues bool, chunkSize int64) (result string, explodedArgs []interface{}, err error) {
	if len(sharedColumns) == 0 {
		return "", explodedArgs, fmt.Errorf("Got 0 shared columns in BuildRangeSelectQuery")
	}
	databaseName = EscapeName(databaseName)
	originalTableName = EscapeName(originalTableName)
	ghostTableName = EscapeName(ghostTableName)

	/*mappedSharedColumns = duplicateNames(mappedSharedColumns)
	for i := range mappedSharedColumns {
		mappedSharedColumns[i] = EscapeName(mappedSharedColumns[i])
	}
	mappedSharedColumnsListing := strings.Join(mappedSharedColumns, ", ")*/

	sharedColumns = duplicateNames(sharedColumns)
	for i := range sharedColumns {
		sharedColumns[i] = EscapeName(sharedColumns[i])
	}
	sharedColumnsListing := strings.Join(sharedColumns, ", ")

	uniqueKey = EscapeName(uniqueKey)
	/*var minRangeComparisonSign ValueComparisonSign = GreaterThanComparisonSign
	if includeRangeStartValues {
		minRangeComparisonSign = GreaterThanOrEqualsComparisonSign
	}
	rangeStartComparison, rangeExplodedArgs, err := BuildRangeComparison(uniqueKeyColumns.Names(), rangeStartValues, rangeStartArgs, minRangeComparisonSign)
	if err != nil {
		return "", explodedArgs, err
	}
	explodedArgs = append(explodedArgs, rangeExplodedArgs...)*/
	rangeEndComparison, rangeExplodedArgs, err := BuildRangeComparison(uniqueKeyColumns.Names(), rangeEndValues, rangeEndArgs, LessThanOrEqualsComparisonSign)
	if err != nil {
		return "", explodedArgs, err
	}
	explodedArgs = append(explodedArgs, rangeExplodedArgs...)

	uniqueKeyColumnNames := duplicateNames(uniqueKeyColumns.Names())
	uniqueKeyColumnAscending := make([]string, len(uniqueKeyColumnNames), len(uniqueKeyColumnNames))
	uniqueKeyColumnDescending := make([]string, len(uniqueKeyColumnNames), len(uniqueKeyColumnNames))
	for i, column := range uniqueKeyColumns.ColumnList() {
		uniqueKeyColumnNames[i] = EscapeName(uniqueKeyColumnNames[i])
		if column.Type == umconf.EnumColumnType {
			uniqueKeyColumnAscending[i] = fmt.Sprintf("concat(%s) asc", uniqueKeyColumnNames[i])
			uniqueKeyColumnDescending[i] = fmt.Sprintf("concat(%s) desc", uniqueKeyColumnNames[i])
		} else {
			uniqueKeyColumnAscending[i] = fmt.Sprintf("%s asc", uniqueKeyColumnNames[i])
			uniqueKeyColumnDescending[i] = fmt.Sprintf("%s desc", uniqueKeyColumnNames[i])
		}
	}
	/*result = fmt.Sprintf(`
		select %s
			from
				%s.%s
			force index (%s)
			where (%s and %s)
			order by
				%s
			limit %d
	    `, sharedColumnsListing, databaseName, originalTableName,
			uniqueKey,rangeStartComparison, rangeEndComparison,
			strings.Join(uniqueKeyColumnAscending, ", "), chunkSize,
		)*/
	result = fmt.Sprintf(`
	select %s
		from
			%s.%s
		force index (%s)
		where (%s)
		order by
			%s
		limit %d
    `, sharedColumnsListing, databaseName, originalTableName,
		uniqueKey, rangeEndComparison,
		strings.Join(uniqueKeyColumnAscending, ", "), chunkSize,
	)
	return result, explodedArgs, nil
}

func BuildRangeSelectPreparedQuery(databaseName, originalTableName, ghostTableName string, sharedColumns []string, mappedSharedColumns []string, uniqueKey string, uniqueKeyColumns *umconf.ColumnList, rangeStartArgs, rangeEndArgs []interface{}, includeRangeStartValues bool, chunkSize int64) (result string, explodedArgs []interface{}, err error) {
	rangeStartValues := buildColumnsPreparedValues(uniqueKeyColumns)
	rangeEndValues := buildColumnsPreparedValues(uniqueKeyColumns)
	return BuildRangeSelectQuery(databaseName, originalTableName, ghostTableName, sharedColumns, mappedSharedColumns, uniqueKey, uniqueKeyColumns, rangeStartValues, rangeEndValues, rangeStartArgs, rangeEndArgs, includeRangeStartValues, chunkSize)
}

func BuildUniqueKeyRangeEndPreparedQuery(databaseName, tableName string, uniqueKeyColumns *umconf.ColumnList, rangeStartArgs, rangeEndArgs []interface{}, chunkSize int64, includeRangeStartValues bool, hint string) (result string, explodedArgs []interface{}, err error) {
	if uniqueKeyColumns.Len() == 0 {
		return "", explodedArgs, fmt.Errorf("Got 0 columns in BuildUniqueKeyRangeEndPreparedQuery")
	}
	databaseName = EscapeName(databaseName)
	tableName = EscapeName(tableName)

	var startRangeComparisonSign ValueComparisonSign = GreaterThanComparisonSign
	if includeRangeStartValues {
		startRangeComparisonSign = GreaterThanOrEqualsComparisonSign
	}
	rangeStartComparison, rangeExplodedArgs, err := BuildRangePreparedComparison(uniqueKeyColumns, rangeStartArgs, startRangeComparisonSign)
	if err != nil {
		return "", explodedArgs, err
	}
	explodedArgs = append(explodedArgs, rangeExplodedArgs...)
	rangeEndComparison, rangeExplodedArgs, err := BuildRangePreparedComparison(uniqueKeyColumns, rangeEndArgs, LessThanOrEqualsComparisonSign)
	if err != nil {
		return "", explodedArgs, err
	}
	explodedArgs = append(explodedArgs, rangeExplodedArgs...)

	uniqueKeyColumnNames := duplicateNames(uniqueKeyColumns.Names())
	uniqueKeyColumnAscending := make([]string, len(uniqueKeyColumnNames), len(uniqueKeyColumnNames))
	uniqueKeyColumnDescending := make([]string, len(uniqueKeyColumnNames), len(uniqueKeyColumnNames))
	for i, column := range uniqueKeyColumns.ColumnList() {
		uniqueKeyColumnNames[i] = EscapeName(uniqueKeyColumnNames[i])
		if column.Type == umconf.EnumColumnType {
			uniqueKeyColumnAscending[i] = fmt.Sprintf("concat(%s) asc", uniqueKeyColumnNames[i])
			uniqueKeyColumnDescending[i] = fmt.Sprintf("concat(%s) desc", uniqueKeyColumnNames[i])
		} else {
			uniqueKeyColumnAscending[i] = fmt.Sprintf("%s asc", uniqueKeyColumnNames[i])
			uniqueKeyColumnDescending[i] = fmt.Sprintf("%s desc", uniqueKeyColumnNames[i])
		}
	}
	result = fmt.Sprintf(`
      select %s
				from (
					select
							%s
						from
							%s.%s
						where %s and %s
						order by
							%s
						limit %d
				) select_osc_chunk
			order by
				%s
			limit 1
    `, strings.Join(uniqueKeyColumnNames, ", "),
		strings.Join(uniqueKeyColumnNames, ", "), databaseName, tableName,
		rangeStartComparison, rangeEndComparison,
		strings.Join(uniqueKeyColumnAscending, ", "), chunkSize,
		strings.Join(uniqueKeyColumnDescending, ", "),
	)
	return result, explodedArgs, nil
}

func BuildUniqueKeyMinValuesPreparedQuery(databaseName, tableName string, uniqueKeyColumns *umconf.ColumnList) (string, error) {
	return buildUniqueKeyMinMaxValuesPreparedQuery(databaseName, tableName, uniqueKeyColumns, "asc")
}

func BuildUniqueKeyMaxValuesPreparedQuery(databaseName, tableName string, uniqueKeyColumns *umconf.ColumnList) (string, error) {
	return buildUniqueKeyMinMaxValuesPreparedQuery(databaseName, tableName, uniqueKeyColumns, "desc")
}

func buildUniqueKeyMinMaxValuesPreparedQuery(databaseName, tableName string, uniqueKeyColumns *umconf.ColumnList, order string) (string, error) {
	if uniqueKeyColumns.Len() == 0 {
		return "", fmt.Errorf("Got 0 columns in BuildUniqueKeyMinMaxValuesPreparedQuery")
	}
	databaseName = EscapeName(databaseName)
	tableName = EscapeName(tableName)

	uniqueKeyColumnNames := duplicateNames(uniqueKeyColumns.Names())
	uniqueKeyColumnOrder := make([]string, len(uniqueKeyColumnNames), len(uniqueKeyColumnNames))
	for i, column := range uniqueKeyColumns.ColumnList() {
		uniqueKeyColumnNames[i] = EscapeName(uniqueKeyColumnNames[i])
		if column.Type == umconf.EnumColumnType {
			uniqueKeyColumnOrder[i] = fmt.Sprintf("concat(%s) %s", uniqueKeyColumnNames[i], order)
		} else {
			uniqueKeyColumnOrder[i] = fmt.Sprintf("%s %s", uniqueKeyColumnNames[i], order)
		}
	}
	query := fmt.Sprintf(`
      select %s
				from
					%s.%s
				order by
					%s
				limit 1
    `, strings.Join(uniqueKeyColumnNames, ", "),
		databaseName, tableName,
		strings.Join(uniqueKeyColumnOrder, ", "),
	)
	return query, nil
}

func BuildDMLDeleteQuery(databaseName, tableName string, tableColumns *umconf.ColumnList, args []interface{}) (result string, uniqueKeyArgs []interface{}, err error) {
	if len(args) != tableColumns.Len() {
		return result, uniqueKeyArgs, fmt.Errorf("args count differs from table column count in BuildDMLDeleteQuery")
	}

	for _, column := range tableColumns.ColumnList() {
		tableOrdinal := tableColumns.Ordinals[column.Name]
		arg := column.ConvertArg(args[tableOrdinal])
		uniqueKeyArgs = append(uniqueKeyArgs, arg)
	}
	databaseName = EscapeName(databaseName)
	tableName = EscapeName(tableName)
	equalsComparison, err := BuildEqualsPreparedComparison(tableColumns.Names())
	if err != nil {
		return result, uniqueKeyArgs, err
	}
	result = fmt.Sprintf(`
			delete
				from
					%s.%s
				where
					%s
		`, databaseName, tableName,
		equalsComparison,
	)
	return result, uniqueKeyArgs, nil
}

func BuildDMLInsertQuery(databaseName, tableName string, tableColumns, sharedColumns, mappedSharedColumns *umconf.ColumnList, columnValues []*umconf.ColumnValues) (result string, sharedArgs []interface{}, err error) {
	for _, args := range columnValues {
		if len(args.GetAbstractValues()) != tableColumns.Len() {
			return result, sharedArgs, fmt.Errorf("args count differs from table column count in BuildDMLInsertQuery")
		}
	}

	if !sharedColumns.IsSubsetOf(tableColumns) {
		return result, sharedArgs, fmt.Errorf("shared columns is not a subset of table columns in BuildDMLInsertQuery")
	}
	if sharedColumns.Len() == 0 {
		return result, sharedArgs, fmt.Errorf("No shared columns found in BuildDMLInsertQuery")
	}
	databaseName = EscapeName(databaseName)
	tableName = EscapeName(tableName)

	for _, args := range columnValues {
		for _, column := range sharedColumns.ColumnList() {
			tableOrdinal := tableColumns.Ordinals[column.Name]
			arg := column.ConvertArg(args.GetAbstractValues()[tableOrdinal])
			sharedArgs = append(sharedArgs, arg)
		}
	}

	mappedSharedColumnNames := duplicateNames(mappedSharedColumns.Names())
	for i := range mappedSharedColumnNames {
		mappedSharedColumnNames[i] = EscapeName(mappedSharedColumnNames[i])
	}
	preparedValues := buildColumnsPreparedValues(mappedSharedColumns)

	var buffer bytes.Buffer
	for i := 0; i < len(columnValues); i++ {
		if i == len(columnValues)-1 {
			buffer.WriteString(fmt.Sprintf("(%v)", strings.Join(preparedValues, ", ")))
		} else {
			buffer.WriteString(fmt.Sprintf("(%v),", strings.Join(preparedValues, ", ")))
		}
	}

	result = fmt.Sprintf(`
			insert into
				%s.%s
					(%s)
				values
					%s
		`, databaseName, tableName,
		strings.Join(mappedSharedColumnNames, ", "),
		buffer.String(),
	)
	return result, sharedArgs, nil
}

func BuildDMLUpdateQuery(databaseName, tableName string, tableColumns, sharedColumns, mappedSharedColumns *umconf.ColumnList, columnValues []*umconf.ColumnValues, whereArgs []interface{}) (result string, sharedArgs, uniqueKeyArgs []interface{}, err error) {
	for _, valueArgs := range columnValues {
		if len(valueArgs.GetAbstractValues()) != tableColumns.Len() {
			return result, sharedArgs, uniqueKeyArgs, fmt.Errorf("value args count differs from table column count in BuildDMLUpdateQuery")
		}
	}

	if len(whereArgs) != tableColumns.Len() {
		return result, sharedArgs, uniqueKeyArgs, fmt.Errorf("where args count differs from table column count in BuildDMLUpdateQuery")
	}
	if !sharedColumns.IsSubsetOf(tableColumns) {
		return result, sharedArgs, uniqueKeyArgs, fmt.Errorf("shared columns is not a subset of table columns in BuildDMLUpdateQuery")
	}
	if sharedColumns.Len() == 0 {
		return result, sharedArgs, uniqueKeyArgs, fmt.Errorf("No shared columns found in BuildDMLUpdateQuery")
	}
	databaseName = EscapeName(databaseName)
	tableName = EscapeName(tableName)

	for _, valueArgs := range columnValues {
		for _, column := range sharedColumns.ColumnList() {
			tableOrdinal := tableColumns.Ordinals[column.Name]
			arg := column.ConvertArg(valueArgs.GetAbstractValues()[tableOrdinal])
			sharedArgs = append(sharedArgs, arg)
		}
	}

	for _, column := range tableColumns.ColumnList() {
		tableOrdinal := tableColumns.Ordinals[column.Name]
		arg := column.ConvertArg(whereArgs[tableOrdinal])
		uniqueKeyArgs = append(uniqueKeyArgs, arg)
	}

	setClause, err := BuildSetPreparedClause(mappedSharedColumns)

	equalsComparison, err := BuildEqualsPreparedComparison(tableColumns.Names())
	result = fmt.Sprintf(`
 			update
 					%s.%s
				set
					%s
				where
 					%s
 		`, databaseName, tableName,
		setClause,
		equalsComparison,
	)
	return result, sharedArgs, uniqueKeyArgs, nil
}