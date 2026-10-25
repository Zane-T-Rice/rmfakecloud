import { useEffect, useRef } from "react";
import { BsChevronRight } from "react-icons/bs";
import { Button } from "react-bootstrap";

function Crumbs({ node, onSelect }) {
    if (node.parent) {
        return (<>
            <Crumbs node={node.parent} onSelect={onSelect} />
            <BsChevronRight />
            <Button variant="outline" onClick={() => onSelect(node)}>{node.data.name}</Button>
        </>)
    }
    // No parent means this is the root - render it
    return <Button variant="outline" onClick={() => onSelect(node)}>{node.data.name}</Button>
}

// The path of a node on one line, scrolls sideways when it is too long
export default function NameTag({ node, onSelect }) {
    const ref = useRef(null);
    // the end of the path is the part that matters
    useEffect(() => {
        if (ref.current) {
            ref.current.scrollLeft = ref.current.scrollWidth;
        }
    }, [node]);

    return (
        <div className="breadcrumbs" ref={ref}>
            <Crumbs node={node} onSelect={onSelect} />
        </div>
    );
}
