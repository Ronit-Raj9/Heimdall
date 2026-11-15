# Hand-written fixture in the shape of an upstream domain tools file.
class FixtureRetailTools:
    @is_tool(ToolType.READ)
    def get_order(self, order_id: str):
        return {}

    @is_tool(ToolType.WRITE)
    def return_items(self, order_id: str, item_ids: list):
        return {}
